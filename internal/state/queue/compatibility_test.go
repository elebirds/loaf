package queuestate

import (
    "context"
    "database/sql"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/queue"
    "github.com/elebirds/loaf/internal/wire"
)

func TestFormalQueueSharesLedgerAndNewInputsAcrossPeers(t *testing.T) {
    original := wire.Version
    defer func() { wire.Version = original }()
    ctx := context.Background()
    path := filepath.Join(t.TempDir(), "queue", "state.sqlite3")
    wire.Version = "v1.0.0"
    identity := Identity{Host: "host-user", Binding: "autodl:standard:instance", Version: wire.Version}
    store, err := Open(ctx, path, identity, true)
    if err != nil {
        t.Fatal(err)
    }
    target := store.TargetID
    request := queue.Request{FormatVersion: 1, ID: "q_old", RunID: "r_old", ProjectID: "project", TargetID: target, CreatedAt: time.Now().UTC()}
    if _, err = store.Prepare(ctx, request, queue.Execution{Version: "v1.0.0", Root: "/project", Args: []string{"true"}}); err != nil {
        t.Fatal(err)
    }
    if _, err = store.Admit(ctx, request.ID); err != nil {
        t.Fatal(err)
    }
    store.Close()
    wire.Version, identity.Version = "v1.1.0", "v1.1.0"
    store, err = Open(ctx, path, identity, true)
    if err != nil || store.TargetID != target {
        t.Fatal("new helper did not reuse the ledger", store, err)
    }
    kept, err := store.Entry(ctx, request.ID)
    if err != nil || kept.Execution.Version != "v1.0.0" {
        t.Fatal(kept, err)
    }
    request.ID, request.RunID = "q_new", "r_new"
    if _, err = store.Prepare(ctx, request, queue.Execution{Version: "v1.1.0", Root: "/project", Args: []string{"true"}}); err != nil {
        t.Fatal(err)
    }
    if _, err = store.Admit(ctx, request.ID); err != nil {
        t.Fatal(err)
    }
    store.Close()
    // The original Coordinator can retain ownership and see both generations.
    wire.Version, identity.Version = "v1.0.0", "v1.0.0"
    store, err = Open(ctx, path, identity, true)
    if err != nil {
        t.Fatal("old Coordinator lost its own ledger", err)
    }
    defer store.Close()
    old, err := store.Entry(ctx, "q_old")
    newer, nextErr := store.Entry(ctx, "q_new")
    if err != nil || nextErr != nil || old.Execution.Version != "v1.0.0" || newer.Execution.Version != "v1.1.0" {
        t.Fatal(old, newer, err, nextErr)
    }
    var ownerVersion string
    if err = store.database.QueryRow("SELECT software_version FROM queue_owner").Scan(&ownerVersion); err != nil || ownerVersion != "v1.0.0" {
        t.Fatal("client rewrote the Coordinator origin", ownerVersion, err)
    }
}

func TestFormalQueueRejectsDevelopmentAtTheSameLedger(t *testing.T) {
    original := wire.Version
    defer func() { wire.Version = original }()
    wire.Version = "v1.0.0-rc.1"
    path := filepath.Join(t.TempDir(), "queue", "state.sqlite3")
    identity := Identity{Host: "host-user", Version: wire.Version}
    store, err := Open(context.Background(), path, identity, true)
    if err != nil {
        t.Fatal(err)
    }
    target := store.TargetID
    store.Close()
    wire.Version, identity.Version = "v1.0.0", "v1.0.0"
    for _, write := range []bool{false, true} {
        if s, err := Open(context.Background(), path, identity, write); err == nil {
            s.Close()
            t.Fatal("formal helper adopted development queue", write)
        }
    }
    database, err := sql.Open("sqlite", path)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    var kept, series string
    if err = database.QueryRow("SELECT target_id,compatibility_series FROM queue_owner").Scan(&kept, &series); err != nil || kept != target || series != "development" {
        t.Fatal("refusal replaced the capacity ledger", kept, series, err)
    }
}
