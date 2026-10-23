package run

import (
    "context"
    "errors"
    "slices"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

type recoveryRemote struct {
    cursorRemote
    failures int
    blocked  bool
}

func (r *recoveryRemote) Follow(ctx context.Context, _ config.Target, _ string, req wire.Request, _ bool, emit func(wire.LogEvent) error) error {
    r.calls++
    if r.blocked && r.calls > 1 {
        <-ctx.Done()
        return ctx.Err()
    }
    if r.calls <= r.failures {
        return fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Connection reset by peer"})
    }
    return emit(wire.LogEvent{Type: "end", Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "succeeded"}, Log: wire.LogChunk{Cursor: testCursor(req, 0)}})
}
func TestRecoveryBudgetCoversConsecutiveFailuresAndBoundsBlockedRetry(t *testing.T) {
    for _, blocked := range []bool{false, true} {
        s, in, _, _ := executionFixture(t)
        submitted, err := s.Submit(context.Background(), in, nil)
        if err != nil {
            t.Fatal(err)
        }
        remote := &recoveryRemote{failures: 2, blocked: blocked}
        s.Remote = remote
        progress := []string{}
        budget := 2 * time.Second
        if blocked {
            budget = 60 * time.Millisecond
        }
        started := time.Now()
        _, err = s.FollowRecovering(context.Background(), submitted.Ref, 0, 100, true, "", RecoveryOptions{Budget: budget, Progress: func(v string) { progress = append(progress, v) }}, nil)
        if blocked {
            if err == nil || time.Since(started) > time.Second || !slices.Contains(progress, "exhausted") {
                t.Fatal(err, progress, time.Since(started))
            }
        } else if err != nil || remote.calls != 3 || !slices.Equal(progress, []string{"reconnecting", "recovered"}) {
            t.Fatal(err, remote.calls, progress)
        }
    }
}
func TestObservationSidecarNeverChangesOfflineStatus(t *testing.T) {
    s, in, _, _ := executionFixture(t)
    s.Observations = s.Index.(*config.Store)
    v, err := s.Submit(context.Background(), in, nil)
    if err != nil {
        t.Fatal(err)
    }
    live, err := s.Observe(context.Background(), v.Ref, 0, 0, true)
    if err != nil {
        t.Fatal(err)
    }
    local, err := s.Inspect(in.Project, v.ID)
    if err != nil || local.Status != "unknown" || local.LastObservation == nil || local.LastObservation.Status != live.Status {
        t.Fatal(local, err)
    }
    s.Remote = &recoveryRemote{failures: 100}
    _, _ = s.FollowResumable(context.Background(), v.Ref, 0, 0, true, "", nil)
    local, err = s.Inspect(in.Project, v.ID)
    if err != nil || local.LastObservation.Status != live.Status || local.LastQueryFailure == nil || local.LastQueryFailure.Code != "SSH_FAILED" {
        t.Fatal(local, err)
    }
    failureTime := local.LastQueryFailure.ObservedAt
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    _, _ = s.Follow(ctx, v.Ref, 0, 0, true, nil)
    local, err = s.Inspect(in.Project, v.ID)
    if err != nil || local.LastQueryFailure == nil || !local.LastQueryFailure.ObservedAt.Equal(failureTime) {
        t.Fatal("cancellation replaced the real connection failure", local, err)
    }

}
func TestWaitFrozenSelectionPartialCoverageAndCancellation(t *testing.T) {
    refs := historyRefs()[:3]
    remote := &historyRemote{status: map[string]string{"run-0": "succeeded", "run-1": "failed"}, fail: "run-2"}
    s := Service{Index: historyIndex{refs}, Remote: remote}
    frozen, err := s.Freeze("project", []string{"run-0", "run-1", "run-2"}, nil)
    if err != nil {
        t.Fatal(err)
    }
    result, err := s.Wait(context.Background(), frozen, 25*time.Millisecond, true, nil)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "WAIT_TIMEOUT" || result.Completed != 2 || result.Pending != 1 || result.Examined != 3 || result.QueryErrors != 1 || !result.TimedOut {
        t.Fatal(result, err)
    }
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    _, err = s.Wait(ctx, frozen, time.Minute, true, nil)
    if !errors.As(err, &f) || f.Code != "OBSERVATION_CANCELED" {
        t.Fatal(err)
    }
    for _, ids := range [][]string{{}, {"run-0", "run-0"}, {"missing"}} {
        if _, err := s.Freeze("project", ids, nil); err == nil {
            t.Fatal(ids)
        }
    }
    if remote.launches != 0 {
        t.Fatal("wait launched a task")
    }
}

type interruptedWaitRemote struct {
    localRemote
    mu    sync.Mutex
    calls map[string]int
}

func (r *interruptedWaitRemote) Observe(ctx context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    r.mu.Lock()
    r.calls[req.RunID]++
    n := r.calls[req.RunID]
    r.mu.Unlock()
    if n > 1 {
        <-ctx.Done()
        return wire.Observation{}, ctx.Err()
    }
    if req.RunID == "run-1" {
        return wire.Observation{}, fault.New("SSH_FAILED", "ssh_failed", nil)
    }
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123}}, nil
}
func TestWaitBudgetInterruptionPreservesCompletedObservations(t *testing.T) {
    refs := historyRefs()[:2]
    remote := &interruptedWaitRemote{calls: map[string]int{}}
    service := Service{Index: historyIndex{refs}, Remote: remote}
    result, err := service.Wait(context.Background(), refs, 2100*time.Millisecond, true, nil)
    if err == nil || !result.TimedOut || result.Examined != 2 || result.Interrupted != 2 || result.QueryErrors != 1 || result.Runs[0].Status != "running" || result.Runs[0].QueryError != "" || result.Runs[0].ObservedAt.IsZero() || !result.Runs[0].QueryInterrupted || result.Runs[1].QueryError != "SSH_FAILED" {
        t.Fatal(result, err)
    }
}

type delayedWaitRemote struct{ localRemote }

func (delayedWaitRemote) Observe(ctx context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    timer := time.NewTimer(2100 * time.Millisecond)
    defer timer.Stop()
    select {
    case <-ctx.Done():
        return wire.Observation{}, ctx.Err()
    case <-timer.C:
    }
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "succeeded"}}, nil
}
func TestWaitAllowsOrdinarySSHConnectionAllowance(t *testing.T) {
    refs := historyRefs()[:1]
    service := Service{Index: historyIndex{refs}, Remote: &delayedWaitRemote{}}
    result, err := service.Wait(context.Background(), refs, 4*time.Second, true, nil)
    if err != nil || result.Completed != 1 {
        t.Fatal(result, err)
    }
}
