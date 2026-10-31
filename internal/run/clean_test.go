package run

import (
    "context"
    "errors"
    "reflect"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type cleanupRemote struct {
    Remote
    summary                          wire.RunSummary
    queries, arches, cleans, deploys int
    refuse, lost                     bool
}

func (r *cleanupRemote) Control(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (*wire.RunRecord, error) {
    r.queries++
    if req.Action != "status" {
        return nil, errors.New("unexpected mutation")
    }
    return &wire.RunRecord{FormatVersion: 1, Request: req, Status: r.summary.Status, Phase: r.summary.Phase, CreatedAt: r.summary.CreatedAt, CompletedAt: r.summary.CompletedAt, ExitCode: r.summary.ExitCode}, nil
}
func (r *cleanupRemote) RunnerArch(context.Context, config.Target, bool) (string, error) {
    r.arches++
    return "aarch64", nil
}
func (r *cleanupRemote) Clean(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (*wire.CleanupReceipt, error) {
    r.cleans++
    if r.refuse {
        return nil, &ssh.RemoteError{Code: "CLEAN_BUSY"}
    }
    if r.lost {
        return nil, errors.New("confirmation lost after deletion")
    }
    now := time.Now().UTC()
    return &wire.CleanupReceipt{FormatVersion: 1, Version: req.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, Summary: r.summary, CleanedAt: &now}, nil
}

func cleanupFixture(t *testing.T) (CleanupService, task.RunRef, *cleanupRemote, *local.Store) {
    t.Helper()
    runs, input, _, _ := executionFixture(t)
    submitted, err := runs.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    completed := submitted.Ref.CreatedAt.Add(time.Second)
    remote := &cleanupRemote{summary: wire.RunSummary{Status: "failed", Phase: "command", CreatedAt: submitted.Ref.CreatedAt, CompletedAt: &completed, ExitCode: new(7)}}
    runs.Remote = remote
    store := runs.Index.(*local.Store)
    service := CleanupService{Runs: &runs, Index: store, Remote: remote, Runner: func(context.Context, config.Target, string, bool) (string, error) {
        remote.deploys++
        return "/current/helper", nil
    }}
    return service, submitted.Ref, remote, store
}

func TestCleanupPreviewDoesNotDeployOrWriteAndRefusesUnconfirmed(t *testing.T) {
    service, ref, remote, store := cleanupFixture(t)
    before, err := store.Runs(ref.ProjectID)
    if err != nil {
        t.Fatal(err)
    }
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil || !preview.Eligible || preview.Cleaned || remote.queries != 1 || remote.arches != 0 || remote.deploys != 0 || remote.cleans != 0 {
        t.Fatal(preview, err, remote)
    }
    refs, _ := store.Runs(ref.ProjectID)
    if refs[0].Cleanup != nil {
        t.Fatal(refs)
    }
    after, _ := store.Runs(ref.ProjectID)
    if !reflect.DeepEqual(before, after) {
        t.Fatal("preview changed local state")
    }
    ref.Submission = "uncertain"
    _, err = service.Preview(context.Background(), ref, true, false)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "CLEAN_NOT_CONFIRMED" || remote.queries != 1 {
        t.Fatal(err, remote.queries)
    }
}

func TestCleanupLostConfirmationRetainsSameIDAndTerminalSummary(t *testing.T) {
    service, ref, remote, store := cleanupFixture(t)
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil {
        t.Fatal(err)
    }
    remote.lost = true
    result, err := service.Clean(context.Background(), preview, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "CLEAN_UNCONFIRMED" || result.Cleaned || !result.Run.CleanupPending || result.Run.Status != "failed" || *result.Run.ExitCode != 7 {
        t.Fatal(result, err)
    }
    latest, err := service.Runs.Select(ref.ProjectID, "")
    if err != nil || latest.ID != ref.ID || latest.Cleanup == nil || latest.Cleanup.CleanedAt != nil {
        t.Fatal(latest, err)
    }
    // A stale submission save must not erase the pending recovery record.
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    remote.lost = false
    recovery, err := service.Preview(context.Background(), latest, true, false)
    if err != nil || remote.queries != 1 {
        t.Fatal(recovery, err, remote.queries)
    }
    done, err := service.Clean(context.Background(), recovery, true)
    if err != nil || !done.Cleaned || done.Run.Status != "failed" || done.Run.CleanedAt == nil {
        t.Fatal(done, err)
    }
    latest, _ = service.Runs.Select(ref.ProjectID, "")
    observed, err := service.Runs.Observe(context.Background(), latest, time.Minute, 100, true)
    if err != nil || observed.CleanedAt == nil || observed.Status != "failed" || remote.queries != 1 {
        t.Fatal(observed, err)
    }
    for _, operation := range []func() error{
        func() error { _, err := service.Runs.Stop(context.Background(), latest, nil, true); return err },
        func() error { _, err := service.Runs.Follow(context.Background(), latest, 0, 0, true, nil); return err },
    } {
        err := operation()
        if !errors.As(err, &f) || f.Code != "RUN_CLEANED" {
            t.Fatal(err)
        }
    }
    again, err := service.Clean(context.Background(), done, true)
    if err != nil || !again.Cleaned || remote.cleans != 2 {
        t.Fatal(again, err, remote.cleans)
    }
}

func TestRejectedCleanupDoesNotHideAvailableRunData(t *testing.T) {
    service, ref, remote, store := cleanupFixture(t)
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil {
        t.Fatal(err)
    }
    remote.refuse = true
    _, err = service.Clean(context.Background(), preview, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "CLEAN_BUSY" {
        t.Fatal(err)
    }
    refs, err := store.Runs(ref.ProjectID)
    if err != nil || len(refs) != 1 || refs[0].Cleanup != nil {
        t.Fatal(refs, err)
    }
}

type confirmedObservation struct {
    Remote
    status string
    err    error
}

func (r confirmedObservation) Observe(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    created := time.Now().UTC().Add(-time.Second)
    completed := time.Now().UTC()
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: r.status, Phase: "command", CreatedAt: created, CompletedAt: &completed, ExitCode: new(7), SupervisorPID: 123}}, r.err
}

func TestSuccessfulObservationConfirmsOnlyAcceptedUncertainLocator(t *testing.T) {
    for _, tc := range []struct {
        status string
        err    error
        want   string
    }{
        {"failed", nil, "submitted"},
        {"unknown", nil, "uncertain"},
        {"failed", errors.New("query lost"), "uncertain"},
    } {
        t.Run(tc.want+tc.status, func(t *testing.T) {
            runs, input, _, remote := executionFixture(t)
            remote.lost, remote.unknown = true, true
            submitted, err := runs.Submit(context.Background(), input, nil)
            if err == nil || submitted.Ref.Submission != "uncertain" {
                t.Fatal(submitted, err)
            }
            ref := submitted.Ref
            runs.Remote = confirmedObservation{status: tc.status, err: tc.err}
            _, err = runs.Observe(context.Background(), ref, 0, 0, true)
            if (err != nil) != (tc.err != nil) {
                t.Fatal(err)
            }
            latest, err := runs.Select(ref.ProjectID, "")
            if err != nil || latest.Submission != tc.want || latest.ID != ref.ID {
                t.Fatal(latest, err)
            }
        })
    }
}

func TestRejectedRetryDoesNotEraseEarlierUnconfirmedCleanup(t *testing.T) {
    service, ref, remote, store := cleanupFixture(t)
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil {
        t.Fatal(err)
    }
    remote.lost = true
    if _, err := service.Clean(context.Background(), preview, true); err == nil {
        t.Fatal("lost confirmation was reported as success")
    }
    latest, _ := service.Runs.Select(ref.ProjectID, "")
    preview, err = service.Preview(context.Background(), latest, true, false)
    if err != nil {
        t.Fatal(err)
    }
    remote.refuse = true
    _, err = service.Clean(context.Background(), preview, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "CLEAN_UNCONFIRMED" {
        t.Fatal(err)
    }
    refs, err := store.Runs(ref.ProjectID)
    if err != nil || refs[0].Cleanup == nil || refs[0].Cleanup.CleanedAt != nil {
        t.Fatal(refs, err)
    }
}

type failedCleanupSave struct {
    CleanupIndex
    calls int
}

func (s *failedCleanupSave) RecordCleanup(ctx context.Context, ref task.RunRef, cleanup *task.RunCleanup) error {
    s.calls++
    if s.calls == 2 {
        return errors.New("local storage unavailable")
    }
    return s.CleanupIndex.RecordCleanup(ctx, ref, cleanup)
}

func TestCleanupFinalSaveFailureKeepsPersistentUnconfirmedResult(t *testing.T) {
    service, ref, _, store := cleanupFixture(t)
    service.Index = &failedCleanupSave{CleanupIndex: store}
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil {
        t.Fatal(err)
    }
    result, err := service.Clean(context.Background(), preview, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "CLEAN_UNCONFIRMED" || result.Cleaned || result.Run.CleanedAt != nil || !result.Run.CleanupPending {
        t.Fatal(result, err)
    }
    latest, err := service.Runs.Select(ref.ProjectID, "")
    if err != nil || latest.Cleanup == nil || latest.Cleanup.CleanedAt != nil {
        t.Fatal(latest, err)
    }
}

func TestCleanupPreviewRepairsOnlyWhenExplicitlyRequested(t *testing.T) {
    service, ref, _, store := cleanupFixture(t)
    repairs := 0
    service.Runs.Runner = func(_ context.Context, _ config.Target, arch string, _ bool) (string, error) {
        if arch != "" {
            t.Fatal(arch)
        }
        repairs++
        return "/verified/current/helper", nil
    }
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil || !preview.Eligible || repairs != 0 {
        t.Fatal(preview, err, repairs)
    }
    ready, err := service.Preview(context.Background(), ref, true, true)
    if err != nil || !ready.Eligible || repairs != 1 || ready.Ref.Runner != ref.Runner {
        t.Fatal(ready, err, repairs)
    }
    saved, err := store.Runs(ref.ProjectID)
    if err != nil || saved[0].Runner != ref.Runner || saved[0].Cleanup != nil {
        t.Fatal(saved, err)
    }
}
