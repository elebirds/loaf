package queuestate

import (
    "context"
    "database/sql"
    "fmt"
    "github.com/elebirds/loaf/internal/testutil"
    "path/filepath"
    "slices"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
)

func newStore(t *testing.T) *Store {
    t.Helper()
    ctx := context.Background()
    s, err := Open(ctx, filepath.Join(t.TempDir(), "queue", "state.sqlite3"), Identity{Host: "host-user-1", Binding: "standard:instance-1"}, true)
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { s.Close() })
    return s
}

func prepare(t *testing.T, s *Store, id, project string) Entry {
    t.Helper()
    now := time.Now().UTC()
    e, err := s.Prepare(context.Background(), queue.Request{FormatVersion: 1, ID: id, ProjectID: project, RunID: "r_" + id, TargetID: s.TargetID, CreatedAt: now}, queue.Execution{Version: "test", Root: "/remote/" + project, Args: []string{"python3", "train.py"}, Env: map[string]string{"SECRET": "private"}, Outputs: []string{"outputs"}})
    if err != nil {
        t.Fatal(err)
    }
    return e
}

func TestAdmissionFIFOSharedCapacityAndDurableEvents(t *testing.T) {
    ctx := context.Background()
    s := newStore(t)
    prepare(t, s, "q_1", "project_1")
    prepare(t, s, "q_2", "project_2")
    if _, err := s.Admit(ctx, "q_2"); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Admit(ctx, "q_1"); err != nil {
        t.Fatal(err)
    }
    selected, err := s.Reserve(ctx, queue.FIFO{})
    if err != nil || len(selected) != 1 || selected[0].Request.ID != "q_2" {
        t.Fatalf("selected=%v error=%v", selected, err)
    }
    if again, err := s.Reserve(ctx, queue.FIFO{}); err != nil || len(again) != 0 {
        t.Fatalf("oversubscribed: %v %v", again, err)
    }
    if _, err := s.MarkAttempted(ctx, "q_2"); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Unconfirmed(ctx, "q_2", "QUEUE_DISPATCH_UNCONFIRMED"); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Unconfirmed(ctx, "q_2", "QUEUE_DISPATCH_UNCONFIRMED"); err != nil {
        t.Fatal(err)
    }
    page, err := s.Events(ctx, event.ReadInput{Limit: 100})
    if err != nil || len(page.Events) != 3 {
        t.Fatalf("facts=%v error=%v", page.Events, err)
    }
    for _, fact := range page.Events {
        if err := fact.Validate(); err != nil {
            t.Fatal(err)
        }
    }
    if _, err := s.Confirm(ctx, "q_2", true); err != nil {
        t.Fatal(err)
    }
    selected, err = s.Reserve(ctx, queue.FIFO{})
    if err != nil || len(selected) != 1 || selected[0].Request.ID != "q_1" {
        t.Fatalf("did not advance FIFO: %v %v", selected, err)
    }
}

func TestCanceledRequestRetainsNoStartAndPrivateFrozenInput(t *testing.T) {
    ctx := context.Background()
    s := newStore(t)
    prepared := prepare(t, s, "q_cancel", "project_1")
    if _, err := s.Cancel(ctx, "q_cancel"); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Admit(ctx, "q_cancel"); err == nil {
        t.Fatal("canceled request readmitted")
    }
    if _, err := s.MarkAttempted(ctx, "q_cancel"); err == nil {
        t.Fatal("canceled request attempted")
    }
    existing, err := s.Prepare(ctx, prepared.Request, prepared.Execution)
    if err != nil || existing.Request.State != "canceled" {
        t.Fatalf("identity was replaced: %v %v", existing.Request, err)
    }
    changed := prepared.Execution
    changed.Args = []string{"different"}
    if _, err = s.Prepare(ctx, prepared.Request, changed); err == nil {
        t.Fatal("frozen input replaced")
    }
    page, err := s.Events(ctx, event.ReadInput{Limit: 10})
    if err != nil || len(page.Events) != 1 || page.Events[0].Type != "queue.canceled" {
        t.Fatalf("bad cancel facts: %v %v", page, err)
    }
}

func TestIdentityRefusesCloneAndCursorIncarnation(t *testing.T) {
    ctx := context.Background()
    s := newStore(t)
    path := s.Path
    if _, err := Open(ctx, path, Identity{Host: "other-host", Binding: "standard:instance-1"}, true); err == nil {
        t.Fatal("cloned host accepted")
    }
    if _, err := Open(ctx, path, Identity{Host: "host-user-1", Binding: "standard:instance-2"}, true); err == nil {
        t.Fatal("cloned cloud instance accepted")
    }
    alias, err := Open(ctx, path, Identity{Host: "host-user-1"}, false)
    if err != nil {
        t.Fatal(err)
    }
    defer alias.Close()
    if alias.TargetID != s.TargetID {
        t.Fatal("static alias split machine identity")
    }
    page, err := s.Events(ctx, event.ReadInput{Limit: 1})
    if err != nil {
        t.Fatal(err)
    }
    other := newStore(t)
    if _, err = other.Events(ctx, event.ReadInput{Limit: 1, Cursor: page.NextCursor}); err == nil {
        t.Fatal("cursor crossed queue incarnation")
    }
}

func TestCancelAndReservationHaveOneWinner(t *testing.T) {
    ctx := context.Background()
    s := newStore(t)
    prepare(t, s, "q_race", "project_1")
    s.Admit(ctx, "q_race")
    other, err := Open(ctx, s.Path, Identity{Host: "host-user-1"}, true)
    if err != nil {
        t.Fatal(err)
    }
    defer other.Close()
    start := make(chan struct{})
    var wg sync.WaitGroup
    wg.Add(2)
    go func() { defer wg.Done(); <-start; s.Cancel(ctx, "q_race") }()
    go func() { defer wg.Done(); <-start; other.Reserve(ctx, queue.FIFO{}) }()
    close(start)
    wg.Wait()
    e, err := s.Entry(ctx, "q_race")
    if err != nil {
        t.Fatal(err)
    }
    if e.Request.State != "canceled" && e.Request.State != "dispatching" {
        t.Fatalf("invalid race state: %s", e.Request.State)
    }
    if e.Request.State == "canceled" {
        if _, err := s.MarkAttempted(ctx, "q_race"); err == nil {
            t.Fatal("canceled race winner launched")
        }
    }
}

func TestQueueDatabasePrivateAndUnknownSchemaUnchanged(t *testing.T) {
    s := newStore(t)
    path := s.Path
    s.Close()
    testutil.Private(t, path, false)
    database, err := sql.Open("sqlite", path)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = database.Exec("PRAGMA user_version=99"); err != nil {
        t.Fatal(err)
    }
    database.Close()
    if _, err = Open(context.Background(), path, Identity{Host: "host-user-1"}, true); err == nil {
        t.Fatal("unknown schema mutated")
    }
    database, _ = sql.Open("sqlite", path)
    defer database.Close()
    var version int
    database.QueryRow("PRAGMA user_version").Scan(&version)
    if version != 99 {
        t.Fatal("unknown schema overwritten")
    }
}

func TestQueueListKeepsCurrentWorkAheadOfBoundedHistory(t *testing.T) {
    ctx := context.Background()
    s := newStore(t)
    for i := 0; i < 25; i++ {
        id := fmt.Sprintf("q_history_%02d", i)
        prepare(t, s, id, "project")
        if _, err := s.Admit(ctx, id); err != nil {
            t.Fatal(err)
        }
        if _, err := s.Cancel(ctx, id); err != nil {
            t.Fatal(err)
        }
    }
    prepare(t, s, "q_active", "project")
    if _, err := s.Admit(ctx, "q_active"); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Reserve(ctx, queue.FIFO{}); err != nil {
        t.Fatal(err)
    }
    for _, id := range []string{"q_second", "q_first"} {
        prepare(t, s, id, "project")
        if _, err := s.Admit(ctx, id); err != nil {
            t.Fatal(err)
        }
    }
    prepare(t, s, "q_preparing", "project")
    entries, more, err := s.List(ctx, 4)
    if err != nil || !more {
        t.Fatalf("coverage: more=%v error=%v", more, err)
    }
    ids := []string{}
    for _, e := range entries {
        ids = append(ids, e.Request.ID)
    }
    if !slices.Equal(ids, []string{"q_active", "q_second", "q_first", "q_preparing"}) {
        t.Fatalf("current work buried or FIFO order lost: %v", ids)
    }
    entries, more, err = s.List(ctx, 29)
    if err != nil || more || len(entries) != 29 {
        t.Fatalf("exact coverage: count=%d more=%v error=%v", len(entries), more, err)
    }
    if _, _, err = s.List(ctx, 0); err == nil {
        t.Fatal("unbounded list accepted")
    }
}

func TestCancelBeforePrepareRetainsNoStartIdentity(t *testing.T) {
    s := newStore(t)
    ctx := context.Background()
    r := queue.Request{FormatVersion: 1, ID: "q_never_prepared", RunID: "r_never_prepared", ProjectID: "project", TargetID: s.TargetID, CreatedAt: time.Now().UTC()}
    e := queue.Execution{Version: "test", Root: "/private/project"}
    canceled, err := s.CancelUnprepared(ctx, r, e)
    if err != nil || canceled.Request.State != "canceled" {
        t.Fatal(canceled, err)
    }
    again, err := s.CancelUnprepared(ctx, r, e)
    if err != nil || again.Request.ID != r.ID {
        t.Fatal(again, err)
    }
    delayed, err := s.Prepare(ctx, r, e)
    if err != nil || delayed.Request.State != "canceled" {
        t.Fatal("delayed prepare lost cancellation", delayed, err)
    }
    if _, err = s.Admit(ctx, r.ID); err == nil {
        t.Fatal("canceled input admitted")
    }
}
