package coordinator

import (
    "context"
    "errors"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/queue"
    queuestate "github.com/elebirds/loaf/internal/state/queue"
)

type runs struct {
    launches         []string
    outcome          map[string]Outcome
    loseConfirmation bool
}

func (r *runs) Launch(_ context.Context, e queuestate.Entry) (Outcome, error) {
    r.launches = append(r.launches, e.Request.RunID)
    if r.loseConfirmation {
        return Outcome{}, errors.New("lost confirmation")
    }
    return Outcome{Accepted: true}, nil
}
func (r *runs) Observe(_ context.Context, e queuestate.Entry) (Outcome, error) {
    return r.outcome[e.Request.RunID], nil
}

func TestRestartNeverReplaysAttemptedLaunchAndRetainsCapacity(t *testing.T) {
    ctx := context.Background()
    path := filepath.Join(t.TempDir(), "queue", "state.sqlite3")
    identity := queuestate.Identity{Host: "host-uid"}
    s, err := queuestate.Open(ctx, path, identity, true)
    if err != nil {
        t.Fatal(err)
    }
    for _, id := range []string{"q_1", "q_2"} {
        now := time.Now().UTC()
        _, err = s.Prepare(ctx, queue.Request{FormatVersion: 1, ID: id, RunID: "r_" + id, ProjectID: "project", TargetID: s.TargetID, CreatedAt: now}, queue.Execution{Version: "test", Root: "/tmp/project", Args: []string{"command"}})
        if err != nil {
            t.Fatal(err)
        }
        if _, err = s.Admit(ctx, id); err != nil {
            t.Fatal(err)
        }
    }
    adapter := &runs{loseConfirmation: true, outcome: map[string]Outcome{}}
    c := Coordinator{Store: s, Policy: queue.FIFO{}, Runs: adapter}
    if err = c.Tick(ctx); err != nil {
        t.Fatal(err)
    }
    s.Close()
    s, err = queuestate.Open(ctx, path, identity, true)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    c.Store = s
    if err = c.Tick(ctx); err != nil {
        t.Fatal(err)
    }
    if len(adapter.launches) != 1 {
        t.Fatalf("uncertain launch replayed: %v", adapter.launches)
    }
    status, _ := s.Status(ctx)
    if status.Reserved != 1 || status.Queued != 1 {
        t.Fatalf("uncertainty released slot: %+v", status)
    }
    adapter.loseConfirmation = false
    adapter.outcome["r_q_1"] = Outcome{Accepted: true, Terminal: true}
    if err = c.Tick(ctx); err != nil {
        t.Fatal(err)
    }
    if len(adapter.launches) != 2 || adapter.launches[1] != "r_q_2" {
        t.Fatalf("did not dispatch successor after verified terminal: %v", adapter.launches)
    }
}

type unsafePolicy struct{}

func (unsafePolicy) Select([]queue.Candidate, int) ([]string, error) { return []string{"foreign"}, nil }

func TestPolicyCannotExecuteForeignRequest(t *testing.T) {
    ctx := context.Background()
    s, err := queuestate.Open(ctx, filepath.Join(t.TempDir(), "queue", "state.sqlite3"), queuestate.Identity{Host: "host"}, true)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    adapter := &runs{outcome: map[string]Outcome{}}
    if err = (Coordinator{Store: s, Policy: unsafePolicy{}, Runs: adapter}).Tick(ctx); err == nil {
        t.Fatal("unsafe policy accepted")
    }
    if len(adapter.launches) != 0 {
        t.Fatal("policy bypassed execution admission")
    }
}

type mutatingPolicy struct{}

func (mutatingPolicy) Select(candidates []queue.Candidate, _ int) ([]string, error) {
    candidates[0].ID = "foreign"
    return []string{"foreign"}, nil
}

func TestPolicyMutationCannotRewriteTrustedEligibility(t *testing.T) {
    ctx := context.Background()
    s, err := queuestate.Open(ctx, filepath.Join(t.TempDir(), "queue", "state.sqlite3"), queuestate.Identity{Host: "host"}, true)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    now := time.Now().UTC()
    _, err = s.Prepare(ctx, queue.Request{FormatVersion: 1, ID: "q_1", RunID: "r_1", ProjectID: "project", TargetID: s.TargetID, CreatedAt: now}, queue.Execution{Version: "test", Root: "/tmp/project", Args: []string{"command"}})
    if err != nil {
        t.Fatal(err)
    }
    if _, err = s.Admit(ctx, "q_1"); err != nil {
        t.Fatal(err)
    }
    adapter := &runs{outcome: map[string]Outcome{}}
    if err = (Coordinator{Store: s, Policy: mutatingPolicy{}, Runs: adapter}).Tick(ctx); err == nil {
        t.Fatal("policy rewrote trusted eligible IDs")
    }
    e, err := s.Entry(ctx, "q_1")
    if err != nil || e.Request.State != "queued" || len(adapter.launches) != 0 {
        t.Fatalf("invalid policy changed admission: %+v %v", e, err)
    }
}
