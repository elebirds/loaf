package client

import (
    "context"
    "errors"
    "fmt"
    "reflect"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

func TestUXGCleanRejectsEmptyIDsBeforeSelection(t *testing.T) {
    for _, ids := range [][]string{{""}, {"r_explicit", ""}} {
        _, err := (&Projects{}).PrepareClean(context.Background(), CleanInput{IDs: ids})
        var failure *fault.Error
        if !errors.As(err, &failure) || failure.Code != "BAD_CONFIG" {
            t.Fatalf("empty ID could reach latest selection: %v: %v", ids, err)
        }
    }
}

type uxGCleaner struct {
    previews []task.RunRef
    repairs  []bool
    cleans   []execution.CleanResult
    preview  func(task.RunRef) error
    clean    func(execution.CleanResult) (execution.CleanResult, error)
}

func (c *uxGCleaner) Preview(_ context.Context, ref task.RunRef, _, prepare bool) (execution.CleanResult, error) {
    c.previews = append(c.previews, ref)
    c.repairs = append(c.repairs, prepare)
    result := execution.CleanResult{Run: execution.View{ID: ref.ID, Target: ref.Target, Status: "failed", Submission: ref.Submission, CreatedAt: ref.CreatedAt, CompletedAt: new(ref.CreatedAt.Add(time.Second)), ExitCode: new(7)}, Path: ref.RemoteDir + "/runs/" + ref.ID, Eligible: true, Ref: ref}
    if c.preview != nil {
        return result, c.preview(ref)
    }
    return result, nil
}

func (c *uxGCleaner) Clean(_ context.Context, item execution.CleanResult, _ bool) (execution.CleanResult, error) {
    c.cleans = append(c.cleans, item)
    if c.clean != nil {
        return c.clean(item)
    }
    item.Cleaned = true
    return item, nil
}

func uxGCleanupFixture(t *testing.T, count int) (*Projects, *uxGCleaner, []task.RunRef) {
    t.Helper()
    store := config.New(t.TempDir(), "", "")
    state := local.New(store.StateDir)
    cwd, _, err := config.Discover(context.Background(), t.TempDir())
    if err != nil {
        t.Fatal(err)
    }
    project, err := state.Select(context.Background(), cwd, "original")
    if err != nil {
        t.Fatal(err)
    }
    refs := []task.RunRef{}
    for i := 0; i < count; i++ {
        now := time.Now().UTC().Add(time.Duration(i) * time.Second)
        ref := task.RunRef{ID: fmt.Sprintf("r_%024x", i+1), ProjectID: project.ID, Target: "original", Connection: config.Target{Destination: "fixture", RemoteBase: "/managed"}, RemoteDir: "/managed/" + project.ID, Runner: "/managed/runner", Version: wire.Version, Submission: "submitted", CreatedAt: now, AttemptedAt: &now, Outputs: []string{"results"}}
        if err := state.SaveRun(context.Background(), ref); err != nil {
            t.Fatal(err)
        }
        refs = append(refs, ref)
    }
    cleaner := &uxGCleaner{}
    return &Projects{Config: store, State: state, CWD: cwd, Runs: &execution.Service{Index: state}, Cleaner: cleaner}, cleaner, refs
}

func TestUXGPrepareCleanKeepsMixedProjectCoverageAndNoWrites(t *testing.T) {
    c, cleaner, refs := uxGCleanupFixture(t, 2)
    other, err := c.State.Select(context.Background(), t.TempDir(), "other")
    if err != nil {
        t.Fatal(err)
    }
    foreign := refs[0]
    foreign.ID, foreign.ProjectID = "r_ffffffffffffffffffffffff", other.ID
    if err := c.State.SaveRun(context.Background(), foreign); err != nil {
        t.Fatal(err)
    }
    cleaner.preview = func(ref task.RunRef) error {
        if ref.ID == refs[1].ID {
            return fault.New("CLEAN_NOT_TERMINAL", "clean_not_terminal", nil)
        }
        return nil
    }
    before, err := c.State.Runs(refs[0].ProjectID)
    if err != nil {
        t.Fatal(err)
    }
    batch, err := c.PrepareClean(context.Background(), CleanInput{IDs: []string{refs[0].ID, foreign.ID, refs[1].ID, refs[0].ID}})
    if err == nil || len(batch.Runs) != 3 || !batch.DryRun || !batch.Runs[0].Eligible || batch.Runs[1].ErrorCode != "RUN_NOT_FOUND" || batch.Runs[2].Eligible || batch.Runs[2].ErrorCode != "CLEAN_NOT_TERMINAL" {
        t.Fatal(batch, err)
    }
    if len(cleaner.previews) != 2 || !reflect.DeepEqual(cleaner.repairs, []bool{false, false}) || len(cleaner.cleans) != 0 {
        t.Fatal(cleaner)
    }
    if _, err := c.ExecuteClean(context.Background(), batch, CallPolicy{}); err == nil || len(cleaner.cleans) != 0 {
        t.Fatal("mixed eligibility reached deletion", err, cleaner.cleans)
    }
    after, err := c.State.Runs(refs[0].ProjectID)
    if err != nil || !reflect.DeepEqual(before, after) {
        t.Fatal("preview mutated saved runs", before, after, err)
    }
    // Empty IDs must not fall through to this project's latest saved task.
    beforeCalls := len(cleaner.previews)
    if _, err := c.PrepareClean(context.Background(), CleanInput{IDs: []string{refs[0].ID, ""}}); err == nil || len(cleaner.previews) != beforeCalls {
        t.Fatal("empty ID caused a latest preview", err, cleaner.previews)
    }
}

func TestUXGExecuteCleanKeepsFrozenBindingAndChangedEligibility(t *testing.T) {
    c, cleaner, refs := uxGCleanupFixture(t, 4)
    ids := []string{refs[0].ID, refs[1].ID, refs[2].ID, refs[3].ID}
    preview, err := c.PrepareClean(context.Background(), CleanInput{IDs: ids})
    if err != nil {
        t.Fatal(err)
    }
    if _, err := c.State.Select(context.Background(), c.CWD, "replacement"); err != nil {
        t.Fatal(err)
    }
    latest := refs[0]
    latest.ID = "r_ffffffffffffffffffffffff"
    latest.AttemptedAt = new(time.Now().UTC().Add(time.Hour))
    if err := c.State.SaveRun(context.Background(), latest); err != nil {
        t.Fatal(err)
    }
    refusals := []string{"", "CLEAN_NOT_TERMINAL", "CLEAN_BUSY", "BAD_FORMAT"}
    cleaner.clean = func(item execution.CleanResult) (execution.CleanResult, error) {
        index := len(cleaner.cleans) - 1
        if !reflect.DeepEqual(item.Ref, refs[index]) {
            t.Fatal("execution redirected the saved binding", item.Ref, refs[index])
        }
        if refusals[index] != "" {
            return item, fault.New(refusals[index], "clean_failed", nil)
        }
        item.Cleaned = true
        return item, nil
    }
    result, err := c.ExecuteClean(context.Background(), preview, CallPolicy{})
    if err == nil || result.DryRun || len(result.Runs) != 4 || !result.Runs[0].Cleaned || len(cleaner.cleans) != 4 {
        t.Fatal(result, err)
    }
    for i, item := range result.Runs {
        if item.Run.ID != ids[i] || item.ErrorCode != refusals[i] || item.Cleaned != (i == 0) {
            t.Fatal("lost failed row", item)
        }
    }
    if preview.Runs[0].Cleaned || preview.Runs[1].ErrorCode != "" {
        t.Fatal("execution changed the caller's preview", preview)
    }
}

func TestUXGExecuteCleanCancellationPreservesUnconfirmedAndUnattempted(t *testing.T) {
    c, cleaner, refs := uxGCleanupFixture(t, 3)
    preview, err := c.PrepareClean(context.Background(), CleanInput{IDs: []string{refs[0].ID, refs[1].ID, refs[2].ID}})
    if err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    cleaner.clean = func(item execution.CleanResult) (execution.CleanResult, error) {
        cancel()
        item.Run.CleanupPending = true
        return item, fault.New("CLEAN_UNCONFIRMED", "clean_unconfirmed", nil)
    }
    result, err := c.ExecuteClean(ctx, preview, CallPolicy{})
    if err == nil || len(cleaner.cleans) != 1 || len(result.Runs) != 3 || result.Runs[0].ErrorCode != "CLEAN_UNCONFIRMED" || !result.Runs[0].Run.CleanupPending {
        t.Fatal(result, err)
    }
    for _, item := range result.Runs[1:] {
        if item.Cleaned || item.Run.CleanupPending || item.ErrorCode != "CLEAN_NOT_ATTEMPTED" || item.Path == "" {
            t.Fatal("unattempted row was lost or marked cleaned", item)
        }
    }
}

type uxGPreviewRemote struct {
    execution.Remote
    queries int
}

func (r *uxGPreviewRemote) Control(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (*wire.RunRecord, error) {
    if req.Action != "status" {
        return nil, errors.New("preview attempted a mutation")
    }
    r.queries++
    return &wire.RunRecord{FormatVersion: wire.FormatVersion, Request: req, Status: "running", Phase: "command", CreatedAt: time.Now().UTC(), SupervisorPID: 123}, nil
}

func TestUXGCachedTerminalDoesNotAuthorizePreparingUncertainOrRunning(t *testing.T) {
    c, _, refs := uxGCleanupFixture(t, 1)
    remote := &uxGPreviewRemote{}
    c.Runs.Remote = remote
    c.Cleaner = execution.CleanupService{Runs: c.Runs}
    ids := []string{}
    for i, submission := range []string{"preparing", "uncertain", "submitted"} {
        ref := refs[0]
        ref.ID, ref.Submission = fmt.Sprintf("r_%024x", i+10), submission
        if err := c.State.SaveRun(context.Background(), ref); err != nil {
            t.Fatal(err)
        }
        cached := task.RunObservation{Last: &task.ObservedRun{ObservedAt: time.Now().UTC().Add(-time.Hour), Status: "succeeded", Phase: "command", CompletedAt: new(ref.CreatedAt), ExitCode: new(0)}}
        if err := c.State.SaveRunObservation(context.Background(), ref.ProjectID, ref.ID, cached); err != nil {
            t.Fatal(err)
        }
        ids = append(ids, ref.ID)
    }
    batch, err := c.PrepareClean(context.Background(), CleanInput{IDs: ids})
    if err == nil || len(batch.Runs) != 3 || remote.queries != 1 {
        t.Fatal(batch, err, remote.queries)
    }
    for i, item := range batch.Runs {
        want := "CLEAN_NOT_CONFIRMED"
        if i == 2 {
            want = "CLEAN_NOT_TERMINAL"
        }
        if item.Eligible || item.ErrorCode != want {
            t.Fatal("cached status widened cleanup eligibility", item)
        }
    }
    if _, err := c.ExecuteClean(context.Background(), batch, CallPolicy{}); err == nil {
        t.Fatal("ineligible batch reached deletion")
    }
}
