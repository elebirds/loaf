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
