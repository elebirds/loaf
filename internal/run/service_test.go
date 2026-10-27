package run

import (
    "context"
    "encoding/json"
    "errors"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/local"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type localCheck struct{}

func (localCheck) Check(context.Context, config.Target, string, bool) (ssh.Report, error) {
    return ssh.Report{Arch: "aarch64", RemoteDir: "/remote/project"}, nil
}

type localSender struct {
    err   error
    input ssh.SyncInput
    calls int
}

func (s *localSender) Sync(_ context.Context, _ config.Target, _, _ string, input ssh.SyncInput, _ bool) (ssh.Transfer, error) {
    s.calls++
    s.input = input
    return ssh.Transfer{}, s.err
}

type localRemote struct {
    launches, queries int
    lost, unknown     bool
    request           wire.Request
    reject            string
}

func (r *localRemote) Control(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (*wire.RunRecord, error) {
    if req.Action == "launch" {
        r.launches++
        r.request = req
        if r.reject != "" {
            return nil, &ssh.RemoteError{Code: r.reject}
        }
        if r.lost {
            return nil, errors.New("lost confirmation")
        }
    } else {
        r.queries++
    }
    if r.unknown {
        return nil, errors.New("not reachable")
    }
    return &wire.RunRecord{FormatVersion: 1, Request: r.request, Status: "running", Phase: "command", SupervisorPID: 123}, nil
}
func (r *localRemote) Observe(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "failed", Phase: "command"}}, nil
}
func (r *localRemote) Follow(context.Context, config.Target, string, wire.Request, bool, func(wire.LogEvent) error) error {
    return nil
}

func executionFixture(t *testing.T) (Service, SubmitInput, *localSender, *localRemote) {
    t.Helper()
    store := local.New(t.TempDir())
    project, err := store.Select(context.Background(), t.TempDir(), "")
    if err != nil {
        t.Fatal(err)
    }
    sender := &localSender{}
    remote := &localRemote{}
    service := Service{Index: store, Check: localCheck{}, Runner: func(context.Context, config.Target, string, bool) (string, error) { return "/remote/runner", nil }, Send: sender, Remote: remote}
    input := SubmitInput{Project: project, Target: "lab", Connection: config.Target{Destination: "lab", RemoteBase: "~/projects"}, Plan: sources.Plan{Root: t.TempDir(), Policy: config.SyncPolicy{Mode: "mirror"}}, Args: []string{"python", "a b.py", "$(not-shell)"}, Batch: true}
    return service, input, sender, remote
}

func TestSubmissionRecoversSameIDWithoutReplayingAndHidesEnvironment(t *testing.T) {
    service, input, sender, remote := executionFixture(t)
    remote.lost = true
    input.File.Run.Env = map[string]string{"PRIVATE_TOKEN": "not-public"}
    submitted, err := service.Submit(context.Background(), input, nil)
    if err != nil || remote.launches != 1 || remote.queries != 1 || submitted.ID != sender.input.RunID || remote.request.Args[1] != "a b.py" {
        t.Fatal(submitted, err, remote)
    }
    latest, err := service.Select(input.Project.ID, "")
    if err != nil || latest.ID != submitted.ID || latest.Submission != "submitted" {
        t.Fatal(latest, err)
    }
    encoded, _ := json.Marshal(submitted)
    if strings.Contains(string(encoded), "not-public") || strings.Contains(string(encoded), "PRIVATE_TOKEN") {
        t.Fatal(string(encoded))
    }
    // A proven sync failure cannot replace the previously submitted locator.
    syncFailure := fault.New("SYNC_FAILED", "sync_failed", nil)
    syncFailure.Hint = "sync_retry"
    sender.err = syncFailure
    preparation, err := service.Submit(context.Background(), input, nil)
    after, _ := service.Select(input.Project.ID, "")
    if err == nil || after.ID != latest.ID || remote.launches != 1 {
        t.Fatal(after, err, remote)
    }
    var f *fault.Error
    if !errors.As(err, &f) || f.Hint != "run_sync_retry" || syncFailure.Hint != "sync_retry" {
        t.Fatal(err)
    }
    saved, err := service.Inspect(input.Project, preparation.ID)
    if err != nil || preparation.ID == "" || saved.ID != preparation.ID || saved.Submission != "preparing" || saved.Status != "unknown" {
        t.Fatal("lost preparation identity", preparation, saved, err)
    }
}

func TestUncertainSubmissionRemainsLatestAndDoesNotSelectOlderRun(t *testing.T) {
    service, input, _, remote := executionFixture(t)
    old, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    remote.lost = true
    remote.unknown = true
    current, err := service.Submit(context.Background(), input, nil)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "SUBMISSION_UNKNOWN" || current.ID == "" || current.ID == old.ID || remote.launches != 2 || remote.queries != 1 {
        t.Fatal(current, err, remote)
    }
    latest, err := service.Select(input.Project.ID, "")
    if err != nil || latest.ID != current.ID || latest.Submission != "uncertain" {
        t.Fatal(latest, err)
    }
}

func TestConfirmedSubmissionRejectionDoesNotReplaceLatest(t *testing.T) {
    service, input, _, remote := executionFixture(t)
    accepted, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    remote.reject = "START_FAILED"
    rejected, err := service.Submit(context.Background(), input, nil)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "START_FAILED" || rejected.ID == "" || rejected.ID == accepted.ID || remote.launches != 2 || remote.queries != 0 {
        t.Fatal(rejected, err, remote)
    }
    // Reopen SQLite: a confirmed rejection must retire the persisted attempt,
    // not merely return a preparation-shaped response to this caller.
    store := local.New(service.Index.(*local.Store).Dir)
    service.Index = store
    latest, err := service.Select(input.Project.ID, "")
    if err != nil || latest.ID != accepted.ID {
        t.Fatal(latest, err)
    }
    database, err := db.Open(context.Background(), store.Path, false)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    var submission string
    var attempted *string
    if err := database.QueryRow("SELECT submission,attempted_at FROM runs WHERE id=?", rejected.ID).Scan(&submission, &attempted); err != nil {
        t.Fatal(err)
    }
    if submission != "preparing" || attempted != nil {
        t.Fatal(submission, attempted)
    }
}

type failIndex struct {
    RunIndex
    saves, failAt int
}

func (i *failIndex) SaveRun(ctx context.Context, ref task.RunRef) error {
    i.saves++
    if i.saves == i.failAt {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    return i.RunIndex.SaveRun(ctx, ref)
}

func TestPersistenceFailureCannotStartAndCommandValidationIsLocal(t *testing.T) {
    for _, failAt := range []int{1, 2} {
        service, input, sender, remote := executionFixture(t)
        service.Index = &failIndex{RunIndex: service.Index, failAt: failAt}
        result, err := service.Submit(context.Background(), input, nil)
        if err == nil || remote.launches != 0 || failAt == 1 && sender.calls != 0 {
            t.Fatal(failAt, err, sender, remote)
        }
        if _, err := service.Select(input.Project.ID, ""); err == nil {
            t.Fatal("promoted a preparation failure")
        }
        if failAt == 1 && result.ID != "" {
            t.Fatal("reported an identity that was not saved", result)
        }
        if failAt == 2 {
            saved, err := service.Select(input.Project.ID, result.ID)
            if err != nil || result.ID == "" || result.Submission != "preparing" || result.StateError != "BAD_CONFIG" || saved.Submission != "preparing" {
                t.Fatal("failed intent save hid the prepared identity", result, saved, err)
            }
        }
    }
    service, input, sender, remote := executionFixture(t)
    input.File.Run.Env = map[string]string{"A-B": "invalid"}
    if _, err := service.Submit(context.Background(), input, nil); err == nil || sender.calls != 0 || remote.launches != 0 {
        t.Fatal(err)
    }
}

func TestLatestAmbiguityAndBoundObservation(t *testing.T) {
    service, input, _, _ := executionFixture(t)
    one, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    two, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    ref := two.Ref
    ref.AttemptedAt = one.Ref.AttemptedAt
    if err := service.Index.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    if _, err := service.Select(input.Project.ID, ""); err == nil {
        t.Fatal("selected an ambiguous latest task")
    }
    actual, err := service.Select(input.Project.ID, one.ID)
    if err != nil {
        t.Fatal(err)
    }
    result, err := service.Observe(context.Background(), actual, 0, 100, true)
    if err != nil || result.Target != "lab" || result.Status != "failed" {
        t.Fatal(result, err)
    }
    if request(actual, time.Nanosecond, 100).WaitMillis != 1 {
        t.Fatal("positive wait became an unbounded follow")
    }
}
