package run

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type abandonRemote struct {
    previews, mutations int
    proof               *wire.AbandonReceipt
    lost, partial       bool
    refusal             string
    before              func()
}

func (r *abandonRemote) Abandon(_ context.Context, _ config.Target, _ string, req wire.Request, dry, _ bool) (*wire.AbandonReceipt, error) {
    if dry {
        r.previews++
        if r.proof != nil {
            copy := *r.proof
            return &copy, nil
        }
        return &wire.AbandonReceipt{FormatVersion: 1, Version: wire.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID}, nil
    }
    r.mutations++
    if r.before != nil {
        r.before()
    }
    if r.refusal != "" {
        return nil, &ssh.RemoteError{Code: r.refusal}
    }
    if r.proof == nil {
        now := time.Now().UTC()
        r.proof = &wire.AbandonReceipt{FormatVersion: 1, Version: wire.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, NoStartAt: &now}
    }
    if r.lost {
        r.lost = false
        return nil, errors.New("response lost after no-start")
    }
    if r.partial {
        return r.proof, &ssh.RemoteError{Code: "ABANDON_UNCONFIRMED"}
    }
    now := time.Now().UTC()
    r.proof.CleanedAt = &now
    return r.proof, nil
}

func abandonmentFixture(t *testing.T) (AbandonService, task.RunRef, *abandonRemote, *local.Store) {
    t.Helper()
    runs, input, sender, _ := executionFixture(t)
    sender.err = errors.New("partial copy failed")
    result, err := runs.Submit(context.Background(), input, nil)
    if err == nil || result.ID == "" || result.Submission != "preparing" {
        t.Fatal(result, err)
    }
    state := runs.Index.(*local.Store)
    remote := &abandonRemote{}
    return AbandonService{Runs: &runs, Index: state, Remote: remote}, result.Ref, remote, state
}

func TestAbandonPreviewKeepsStateAndUsesExplicitHelperPermission(t *testing.T) {
    service, ref, remote, state := abandonmentFixture(t)
    prepares := 0
    service.Runs.Runner = func(context.Context, config.Target, string, bool) (string, error) { prepares++; return ref.Runner, nil }
    result, err := service.Preview(context.Background(), ref, true, false)
    saved, _ := state.Run(ref.ProjectID, ref.ID)
    if err != nil || !result.Eligible || remote.previews != 1 || remote.mutations != 0 || prepares != 0 || saved.Abandonment != nil {
        t.Fatal(result, err, remote, saved)
    }
    _, err = service.Preview(context.Background(), ref, true, true)
    if err != nil || prepares != 1 {
        t.Fatal(err, prepares)
    }
    ref.Submission, ref.AttemptedAt = "submitted", &ref.CreatedAt
    _, err = service.Preview(context.Background(), ref, true, false)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "ABANDON_NOT_PREPARED" || remote.previews != 2 {
        t.Fatal(err, remote)
    }
}

func TestAbandonLostConfirmationRestoresSameIdentity(t *testing.T) {
    service, ref, remote, state := abandonmentFixture(t)
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil {
        t.Fatal(err)
    }
    remote.before = func() {
        got, err := state.Run(ref.ProjectID, ref.ID)
        if err != nil || got.Abandonment == nil || got.Abandonment.NoStartAt != nil {
            t.Fatal("intent not saved first", got, err)
        }
    }
    remote.lost = true
    first, err := service.Abandon(context.Background(), preview, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "ABANDON_UNCONFIRMED" || first.NoStartAt != nil || remote.mutations != 1 {
        t.Fatal(first, err)
    }
    remote.before = nil
    saved, _ := state.Run(ref.ProjectID, ref.ID)
    if saved.Abandonment == nil {
        t.Fatal("pending intent lost")
    }
    retry, err := service.Preview(context.Background(), saved, true, false)
    if err != nil || retry.NoStartAt == nil {
        t.Fatal(retry, err)
    }
    done, err := service.Abandon(context.Background(), retry, true)
    if err != nil || done.CleanedAt == nil || done.NoStartAt == nil || done.Run.Submission != "preparing" || done.Run.Status != "unknown" || remote.mutations != 2 {
        t.Fatal(done, err, remote)
    }
    saved, _ = state.Run(ref.ProjectID, ref.ID)
    if saved.Abandonment == nil || saved.Abandonment.CleanedAt == nil {
        t.Fatal(saved)
    }
    if err := AvailabilityError(saved); errorCode(err) != "RUN_ABANDONED" {
        t.Fatal(err)
    }
    observed, err := service.Runs.Observe(context.Background(), saved, 0, 0, true)
    if err != nil || observed.Status != "unknown" || observed.Abandonment == nil || observed.Abandonment.NoStartAt == nil {
        t.Fatal(observed, err)
    }
    if _, err := service.Runs.Select(ref.ProjectID, ""); err == nil {
        t.Fatal("abandoned preparing became latest")
    }
}

type failedAbandonSave struct {
    AbandonIndex
    calls, failAt int
}

func (i *failedAbandonSave) RecordAbandonment(ctx context.Context, ref task.RunRef, next *task.RunAbandonment) error {
    i.calls++
    if i.calls == i.failAt {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    return i.AbandonIndex.RecordAbandonment(ctx, ref, next)
}

func TestAbandonPartialDeletionAndFinalSaveFailureRetainRemoteFacts(t *testing.T) {
    for _, failSave := range []bool{false, true} {
        t.Run(map[bool]string{false: "partial", true: "save"}[failSave], func(t *testing.T) {
            service, ref, remote, state := abandonmentFixture(t)
            preview, err := service.Preview(context.Background(), ref, true, false)
            if err != nil {
                t.Fatal(err)
            }
            if failSave {
                service.Index = &failedAbandonSave{AbandonIndex: state, failAt: 2}
            } else {
                remote.partial = true
            }
            result, err := service.Abandon(context.Background(), preview, true)
            if err == nil || result.NoStartAt == nil || result.Run.Status != "unknown" {
                t.Fatal(result, err)
            }
            if failSave {
                if result.CleanedAt == nil || result.StateError != "BAD_CONFIG" || errorCode(err) != "BAD_CONFIG" {
                    t.Fatal("lost confirmed facts", result, err)
                }
            } else if result.CleanedAt != nil || errorCode(err) != "ABANDON_UNCONFIRMED" {
                t.Fatal(result, err)
            }
            remote.partial = false
            saved, _ := state.Run(ref.ProjectID, ref.ID)
            retry, err := service.Preview(context.Background(), saved, true, false)
            if err != nil {
                t.Fatal(err)
            }
            done, err := service.Abandon(context.Background(), retry, true)
            if err != nil || done.CleanedAt == nil {
                t.Fatal(done, err)
            }
        })
    }
}

func TestAbandonRejectedRequestDoesNotHideLiveRun(t *testing.T) {
    service, ref, remote, state := abandonmentFixture(t)
    preview, err := service.Preview(context.Background(), ref, true, false)
    if err != nil {
        t.Fatal(err)
    }
    remote.refusal = "ABANDON_BUSY"
    _, err = service.Abandon(context.Background(), preview, true)
    if errorCode(err) != "ABANDON_BUSY" {
        t.Fatal(err)
    }
    saved, _ := state.Run(ref.ProjectID, ref.ID)
    if saved.Abandonment != nil {
        t.Fatal("refused intent hid run", saved)
    }
    pending := &task.RunAbandonment{RequestedAt: time.Now().UTC()}
    if err := state.RecordAbandonment(context.Background(), ref, pending); err != nil {
        t.Fatal(err)
    }
    ref.Abandonment = pending
    if err := AvailabilityError(ref); err != nil {
        t.Fatal("unproven intent blocked live status/stop", err)
    }
    observed, err := service.Runs.Observe(context.Background(), ref, 0, 0, true)
    if err != nil || observed.Phase != "command" {
        t.Fatal(observed, err)
    }
}

func TestAbandonFreezeRefusesOnlyConfirmedNoStart(t *testing.T) {
    service, ref, _, state := abandonmentFixture(t)
    ref.Submission, ref.AttemptedAt = "uncertain", &ref.CreatedAt
    if err := state.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    pending := &task.RunAbandonment{RequestedAt: time.Now().UTC()}
    if err := state.RecordAbandonment(context.Background(), ref, pending); err != nil {
        t.Fatal(err)
    }
    selected, err := service.Runs.Freeze(ref.ProjectID, []string{ref.ID}, nil)
    if err != nil || len(selected) != 1 || selected[0].ID != ref.ID {
        t.Fatal("intent blocked observation", selected, err)
    }
    now := time.Now().UTC()
    proof := &task.RunAbandonment{RequestedAt: pending.RequestedAt, NoStartAt: &now}
    if err := state.RecordAbandonment(context.Background(), ref, proof); err != nil {
        t.Fatal(err)
    }
    selected, err = service.Runs.Freeze(ref.ProjectID, []string{ref.ID}, nil)
    if errorCode(err) != "RUN_ABANDONED" || len(selected) != 0 {
        t.Fatal("wait retained impossible execution or changed selection", selected, err)
    }
}
