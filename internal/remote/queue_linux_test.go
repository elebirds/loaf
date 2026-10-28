package remote

import (
    "context"
    "database/sql"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
    queuestate "github.com/elebirds/loaf/internal/state/queue"
    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
)

func queueFixture(t *testing.T) (string, queuestate.Identity, *queuestate.Store) {
    t.Helper()
    t.Setenv("HOME", t.TempDir())
    directory, err := QueueDirectory()
    if err != nil {
        t.Fatal(err)
    }
    identity, err := queueIdentity("")
    if err != nil {
        t.Fatal(err)
    }
    store, err := queuestate.Open(context.Background(), filepath.Join(directory, "state.sqlite3"), identity, true)
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { store.Close() })
    return directory, identity, store
}

func TestQueueFrozenCopyCancellationAndNoStartCleanup(t *testing.T) {
    ctx := context.Background()
    directory, identity, store := queueFixture(t)
    root := managedRoot(t)
    if err := updateCache(root, "project", func(cache string, _ *os.File) error {
        return os.WriteFile(filepath.Join(cache, "input.txt"), []byte("frozen"), 0600)
    }); err != nil {
        t.Fatal(err)
    }
    now := time.Now().UTC()
    request := queue.Request{FormatVersion: 1, ID: "q_example", TargetID: store.TargetID, ProjectID: "project", RunID: "run", CreatedAt: now}
    input := queue.Execution{Version: wire.Version, Root: root, Args: []string{"/bin/sh", "-c", "touch should-not-run"}, Env: map[string]string{"TOKEN": "private"}, Outputs: []string{"outputs"}}
    call := func(command queue.Command) (queue.Result, error) {
        return queueControlAt(ctx, wire.Request{FormatVersion: 1, Version: wire.Version, Queue: &command}, directory, identity, func(string) error { return nil })
    }
    if _, err := call(queue.Command{Action: "prepare", Request: &request, Execution: &input}); err != nil {
        t.Fatal(err)
    }
    e, err := store.Entry(ctx, request.ID)
    if err != nil {
        t.Fatal(err)
    }
    run := queuedExecution(e, "launch")
    if err = QueueRunGuard(run, false); err != nil {
        t.Fatal(err)
    }
    lock, err := lockProject(root)
    if err != nil {
        t.Fatal(err)
    }
    dir, err := runPath(run)
    if err != nil {
        t.Fatal(err)
    }
    _, err = copyWork(run, dir)
    lock.Close()
    if err != nil {
        t.Fatal(err)
    }
    if _, err = call(queue.Command{Action: "enqueue", ID: request.ID}); err != nil {
        t.Fatal(err)
    }
    if err = updateCache(root, "project", func(cache string, _ *os.File) error {
        return os.WriteFile(filepath.Join(cache, "input.txt"), []byte("newer"), 0600)
    }); err != nil {
        t.Fatal(err)
    }
    frozen, err := os.ReadFile(filepath.Join(dir, "work", "input.txt"))
    if err != nil || string(frozen) != "frozen" {
        t.Fatalf("input=%q err=%v", frozen, err)
    }
    if _, err = call(queue.Command{Action: "cancel", ID: request.ID}); err != nil {
        t.Fatal(err)
    }
    if QueueRunGuard(run, true) == nil || QueueRunGuard(run, false) == nil {
        t.Fatal("canceled request admitted delayed launch/copy")
    }
    before, err := os.ReadDir(dir)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = call(queue.Command{Action: "clean-preview", ID: request.ID}); err != nil {
        t.Fatal(err)
    }
    after, err := os.ReadDir(dir)
    if err != nil || len(before) != len(after) {
        t.Fatalf("preview changed metadata: %v %v", after, err)
    }
    if _, err = call(queue.Command{Action: "clean", ID: request.ID}); err != nil {
        t.Fatal(err)
    }
    if _, err = os.Stat(dir); !os.IsNotExist(err) {
        t.Fatalf("payload remains: %v", err)
    }
    if _, err = call(queue.Command{Action: "clean", ID: request.ID}); err != nil {
        t.Fatal(err)
    }
    if QueueRunGuard(run, true) == nil {
        t.Fatal("cleanup removed no-start protection")
    }
    page, err := store.Events(ctx, event.ReadInput{Limit: 20})
    if err != nil || len(page.Events) != 2 {
        t.Fatalf("facts=%+v error=%v", page, err)
    }
}

func TestUnrelatedDirectRunDoesNotDependOnQueueIdentity(t *testing.T) {
    _, _, store := queueFixture(t)
    // Construct the actual clone counterexample: a copied database with an old
    // owner must reject its queued Run while not imposing queue on new direct work.
    database, err := sql.Open("sqlite", store.Path)
    if err != nil {
        t.Fatal(err)
    }
    _, err = database.Exec("UPDATE queue_owner SET host_identity='donor'")
    database.Close()
    if err != nil {
        t.Fatal(err)
    }
    if err = QueueRunGuard(wire.Request{RunID: "new-direct-run"}, true); err != nil {
        t.Fatalf("direct Run acquired queue prerequisite: %v", err)
    }
}

func TestQueueControlRejectsProtocolBeforeOpeningStorage(t *testing.T) {
    directory := filepath.Join(t.TempDir(), "not-created")
    for _, req := range []wire.Request{{FormatVersion: 99, Version: wire.Version, Queue: &queue.Command{Action: "enable"}}, {FormatVersion: 1, Version: "other", Queue: &queue.Command{Action: "enable"}}} {
        if _, err := queueControlAt(context.Background(), req, directory, queuestate.Identity{Host: "host"}, func(string) error { return nil }); err == nil {
            t.Fatal("foreign protocol accepted")
        }
    }
    if _, err := os.Stat(directory); !os.IsNotExist(err) {
        t.Fatalf("invalid request created storage: %v", err)
    }
}

func TestQueueListReturnsCoverageWithoutHidingLiveWork(t *testing.T) {
    ctx := context.Background()
    directory, identity, store := queueFixture(t)
    root := managedRoot(t)
    for _, id := range []string{"q_old", "q_live"} {
        _, err := store.Prepare(ctx, queue.Request{FormatVersion: queue.Version, ID: id, TargetID: store.TargetID, ProjectID: "project", RunID: "run_" + id, CreatedAt: time.Now().UTC()}, queue.Execution{Version: wire.Version, Root: root, Args: []string{"command"}})
        if err != nil {
            t.Fatal(err)
        }
    }
    if _, err := store.Cancel(ctx, "q_old"); err != nil {
        t.Fatal(err)
    }
    command := queue.Command{Action: "list", Limit: 1}
    result, err := queueControlAt(ctx, wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Queue: &command}, directory, identity, func(string) error { return nil })
    if err != nil || !result.HasMore || len(result.Requests) != 1 || result.Requests[0].ID != "q_live" {
        t.Fatalf("list lost coverage: %+v %v", result, err)
    }
}

func TestQueuedExecutionCarriesFrozenAdmissionMetadata(t *testing.T) {
    at := time.Now().UTC()
    e := queuestate.Entry{Request: queue.Request{ID: "q_example", ProjectID: "project", RunID: "run", AttemptedAt: &at}, Execution: queue.Execution{Version: wire.Version, Root: "/tmp/project", Args: []string{"command"}}}
    req := queuedExecution(e, "launch")
    if req.QueueRequestID != e.Request.ID || req.AttemptedAt == nil || !req.AttemptedAt.Equal(at) {
        t.Fatalf("missing admission metadata: %+v", req)
    }
    r := &wire.RunRecord{Request: req, Status: "running", SupervisorPID: 123}
    if !queueOutcome(e, r).Accepted {
        t.Fatal("matching accepted identity rejected")
    }
    r.Request.QueueRequestID = "other"
    if queueOutcome(e, r).Accepted {
        t.Fatal("foreign admission accepted as this Run")
    }
}

func TestQueueCanceledPreparationReclaimsMissingAndPartialCopies(t *testing.T) {
    ctx := context.Background()
    _, _, store := queueFixture(t)
    root := t.TempDir()
    if _, err := initProject(root, "project"); err != nil {
        t.Fatal(err)
    }
    now := time.Now().UTC()
    e, err := store.Prepare(ctx, queue.Request{FormatVersion: 1, ID: "q_partial", TargetID: store.TargetID, ProjectID: "project", RunID: "partial", CreatedAt: now}, queue.Execution{Version: wire.Version, Root: root, Args: []string{"command"}})
    if err != nil {
        t.Fatal(err)
    }
    if _, err = store.Cancel(ctx, e.Request.ID); err != nil {
        t.Fatal(err)
    }
    e, err = store.Entry(ctx, e.Request.ID)
    if err != nil {
        t.Fatal(err)
    }
    if err = cleanQueuedInput(ctx, e, false); err != nil {
        t.Fatalf("missing not-started payload cannot be cleaned: %v", err)
    }
    // failCopy leaves phase=copying with no Supervisor/child. This is safe to
    // reclaim only through the retained canceled request's no-start proof.
    dir := filepath.Join(root, "runs", e.Request.RunID)
    if err = os.MkdirAll(filepath.Join(dir, "work"), 0700); err != nil {
        t.Fatal(err)
    }
    if err = os.WriteFile(filepath.Join(dir, "work", "partial.txt"), []byte("partial"), 0600); err != nil {
        t.Fatal(err)
    }
    state, err := remotestate.Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    req := queuedExecution(e, "launch")
    if err = state.CreateRun(&wire.RunRecord{FormatVersion: 1, Request: req, Status: "failed", Phase: "copying", CreatedAt: now, Reason: "COPY_FAILED"}); err != nil {
        t.Fatal(err)
    }
    state.Close()
    if err = cleanQueuedInput(ctx, e, false); err != nil {
        t.Fatalf("partial not-started payload cannot be cleaned: %v", err)
    }
    if _, err = os.Stat(dir); !os.IsNotExist(err) {
        t.Fatalf("partial payload remains: %v", err)
    }
    retained, err := store.Entry(ctx, e.Request.ID)
    if err != nil || retained.Request.State != "canceled" {
        t.Fatal("cleanup erased no-start identity")
    }
}
