package remotestate

import (
    "database/sql"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/wire"
)

func TestFormalProjectKeepsOriginAcrossControlHelperVersions(t *testing.T) {
    original := wire.Version
    defer func() { wire.Version = original }()
    wire.Version = "v1.0.0"
    root, run := fixture(t)
    store, err := Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    if err = store.CreateRun(run); err != nil {
        t.Fatal(err)
    }
    store.Close()
    wire.Version = "v1.1.0"
    store, err = Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    defer store.Close()
    kept, err := store.Run(run.Request.RunID)
    if err != nil || kept.Request.Version != "v1.0.0" {
        t.Fatal("reader changed the creation identity", kept, err)
    }
    now := time.Now().UTC()
    kept.Status, kept.CompletedAt, kept.ExitCode = "failed", &now, new(7)
    if err = store.SaveRun(kept); err != nil {
        t.Fatal(err)
    }
    receipt := &wire.CleanupReceipt{FormatVersion: 1, Version: kept.Request.Version, Root: root, ProjectID: "project", RunID: kept.Request.RunID, Summary: wire.Summary(kept), Device: 1, Inode: 2}
    if err = store.SaveCleanup(receipt); err != nil {
        t.Fatal(err)
    }
    got, err := store.Cleanup(kept.Request.RunID)
    if err != nil || got.Version != "v1.0.0" || got.Summary.ExitCode == nil || *got.Summary.ExitCode != 7 {
        t.Fatal(got, err)
    }
    if claimed, err := store.HasClaim(kept.Request.RunID); err != nil || !claimed {
        t.Fatal("cleanup lost the original identity claim", claimed, err)
    }
}

func TestFormalProjectRejectsDevelopmentWithoutConversion(t *testing.T) {
    original := wire.Version
    defer func() { wire.Version = original }()
    wire.Version = "v1.0.0-rc.1"
    root, _ := fixture(t)
    wire.Version = "v1.0.0"
    for _, write := range []bool{false, true} {
        if store, err := Open(root, write); err == nil {
            store.Close()
            t.Fatal("formal helper adopted RC project", write)
        }
    }
    if err := Initialize(root, "project"); err == nil {
        t.Fatal("Initialize converted a development root")
    }
    database, err := sql.Open("sqlite", Path(root))
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    var series string
    if err = database.QueryRow("SELECT compatibility_series FROM ownership").Scan(&series); err != nil || series != "development" {
        t.Fatal("development root was overwritten", series, err)
    }
}
