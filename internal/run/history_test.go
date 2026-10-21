package run

import (
    "context"
    "errors"
    "fmt"
    "slices"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

type historyIndex struct{ refs []config.RunRef }

func (i historyIndex) Runs(string) ([]config.RunRef, error)         { return i.refs, nil }
func (i historyIndex) SaveRun(context.Context, config.RunRef) error { return nil }

type historyRemote struct {
    localRemote
    mu     sync.Mutex
    status map[string]string
    seen   []string
    fail   string
    cancel context.CancelFunc
}

func (r *historyRemote) Observe(_ context.Context, target config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    r.mu.Lock()
    r.seen = append(r.seen, req.RunID)
    r.mu.Unlock()
    if target.Destination != "saved-connection" {
        return wire.Observation{}, errors.New("redirected saved connection")
    }
    if req.RunID == r.fail {
        return wire.Observation{}, fault.New("SSH_FAILED", "ssh_failed", nil)
    }
    if r.cancel != nil {
        r.cancel()
    }
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: r.status[req.RunID], SupervisorPID: 123}}, nil
}
func historyRefs() []config.RunRef {
    refs := []config.RunRef{}
    now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
    for i := 0; i < 6; i++ {
        attempted := now.Add(-time.Duration(i) * time.Hour)
        refs = append(refs, config.RunRef{ID: fmt.Sprintf("run-%d", i), ProjectID: "project", Target: "lab", Connection: config.Target{Destination: "saved-connection"}, CreatedAt: now.Add(-48 * time.Hour), AttemptedAt: &attempted, RemoteDir: "/remote/project", Runner: "/runner", Version: "test"})
    }
    return refs
}

func TestHistoryLocalFiltersPrecedeBoundedStatusQueries(t *testing.T) {
    refs := historyRefs()
    refs[0].Target = "other"
    remote := &historyRemote{status: map[string]string{"run-1": "running", "run-2": "running", "run-3": "failed", "run-4": "failed"}}
    service := Service{Index: historyIndex{refs}, Remote: remote}
    result, err := service.History(context.Background(), "project", HistoryFilters{Target: "lab", Since: refs[4].AttemptedAt, Until: refs[1].AttemptedAt, Statuses: []string{"failed"}}, 2, true)
    slices.Sort(remote.seen)
    if err != nil || result.Total != 6 || result.Eligible != 4 || result.Examined != 2 || result.Matched != 0 || result.Unresolved != 0 || !result.Truncated || len(result.Runs) != 0 || len(remote.seen) != 2 || remote.seen[0] != "run-1" || remote.seen[1] != "run-2" {
        t.Fatal(result, err, remote.seen)
    }
    // A larger explicit bound reaches matching older entries, not a hidden
    // second scan behind a small limit. Inclusive timestamps select both ends.
    result, err = service.History(context.Background(), "project", result.Filters, 4, true)
    if err != nil || result.Examined != 4 || result.Matched != 2 || result.Truncated || len(result.Runs) != 2 || result.Runs[1].ID != "run-4" {
        t.Fatal(result, err)
    }
}

func TestHistoryRetainsUnresolvedRowsWithStatusFilter(t *testing.T) {
    refs := historyRefs()[:3]
    remote := &historyRemote{status: map[string]string{"run-1": "failed", "run-2": "succeeded"}, fail: "run-0"}
    service := Service{Index: historyIndex{refs}, Remote: remote}
    result, err := service.History(context.Background(), "project", HistoryFilters{Statuses: []string{"failed"}}, 20, true)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "QUERY_PARTIAL" || result.Matched != 1 || result.Unresolved != 1 || result.Examined != 3 || result.Truncated || len(result.Runs) != 2 || result.Runs[0].Status != "unknown" || result.Runs[0].QueryError != "SSH_FAILED" || result.Runs[1].Status != "failed" {
        t.Fatal(result, err)
    }
    remote.fail = ""
    remote.status["run-0"] = "unknown"
    result, err = service.History(context.Background(), "project", HistoryFilters{Statuses: []string{"unknown", "succeeded"}}, 20, true)
    if err != nil || result.Matched != 2 || result.Unresolved != 0 || len(result.Runs) != 2 {
        t.Fatal(result, err)
    }
}

func TestHistoryCancellationPreservesCompletedObservations(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    remote := &historyRemote{status: map[string]string{"run-0": "running", "run-1": "running", "run-2": "running", "run-3": "running"}, cancel: cancel}
    service := Service{Index: historyIndex{historyRefs()}, Remote: remote}
    result, err := service.History(ctx, "project", HistoryFilters{}, 20, true)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "QUERY_PARTIAL" || result.Examined < 1 || result.Examined > 4 || !result.Truncated || len(remote.seen) != result.Examined || len(result.Runs) != result.Examined {
        t.Fatal(result, err, remote.seen)
    }
}

func TestHistoryRejectsInvalidFiltersBeforeQueries(t *testing.T) {
    refs := historyRefs()
    remote := &historyRemote{}
    service := Service{Index: historyIndex{refs}, Remote: remote}
    for _, filter := range []HistoryFilters{{Statuses: []string{"complete"}}, {Statuses: []string{""}}, {Since: refs[0].AttemptedAt, Until: refs[1].AttemptedAt}} {
        if _, err := service.History(context.Background(), "project", filter, 20, true); err == nil {
            t.Fatal("accepted filters", filter)
        }
    }
    if _, err := service.History(context.Background(), "project", HistoryFilters{}, 0, true); err == nil {
        t.Fatal("accepted unbounded zero")
    }
    if len(remote.seen) != 0 {
        t.Fatal(remote.seen)
    }
}

type blockingHistoryRemote struct {
    localRemote
    entered chan struct{}
    release chan struct{}
    batches chan bool
}

func (r blockingHistoryRemote) Observe(ctx context.Context, _ config.Target, _ string, req wire.Request, batch bool) (wire.Observation, error) {
    r.batches <- batch
    r.entered <- struct{}{}
    select {
    case <-r.release:
    case <-ctx.Done():
        return wire.Observation{}, ctx.Err()
    }
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123}}, nil
}

func TestHistorySerializesInteractiveAuthentication(t *testing.T) {
    for _, batch := range []bool{false, true} {
        t.Run(fmt.Sprint(batch), func(t *testing.T) {
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()
            remote := blockingHistoryRemote{entered: make(chan struct{}, 6), release: make(chan struct{}), batches: make(chan bool, 6)}
            service := Service{Index: historyIndex{historyRefs()}, Remote: &remote}
            done := make(chan struct{})
            var result History
            go func() { defer close(done); result, _ = service.History(ctx, "project", HistoryFilters{}, 20, batch) }()
            workers := 1
            if batch {
                workers = 4
            }
            for i := 0; i < workers; i++ {
                select {
                case <-remote.entered:
                case <-time.After(3 * time.Second):
                    t.Fatal("worker did not reach observation")
                }
            }
            // Cancellation leaves admitted requests blocked until their context
            // closes and prevents additional workers from starting afterward.
            cancel()
            <-done
            if result.Examined != workers || len(remote.batches) != workers || !result.Truncated {
                t.Fatal(result, len(remote.batches))
            }
            for len(remote.batches) > 0 {
                if got := <-remote.batches; got != batch {
                    t.Fatal("changed SSH authentication mode", got, batch)
                }
            }
        })
    }
}

type slowTargetRemote struct {
    localRemote
    fastID string
    cancel context.CancelFunc
}

func (r *slowTargetRemote) Observe(ctx context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    if req.RunID != r.fastID {
        <-ctx.Done()
        return wire.Observation{}, ctx.Err()
    }
    r.cancel()
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "succeeded", SupervisorPID: 123}}, nil
}

func TestHistoryDoesNotLetSlowAliasMonopolizeWorkers(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    refs := historyRefs()[:5]
    refs[4].Target = "available"
    remote := &slowTargetRemote{fastID: refs[4].ID, cancel: cancel}
    service := Service{Index: historyIndex{refs}, Remote: remote}
    result, err := service.History(ctx, "project", HistoryFilters{}, 5, true)
    if err == nil || result.Matched != 1 {
        t.Fatal(result, err)
    }
    found := false
    for _, observed := range result.Runs {
        if observed.ID == refs[4].ID && observed.Status == "succeeded" && observed.QueryError == "" {
            found = true
        }
    }
    if !found {
        t.Fatal("available machine was hidden behind slow alias entries", result)
    }
}
