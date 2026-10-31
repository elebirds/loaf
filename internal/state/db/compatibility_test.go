package db

import (
    "context"
    "database/sql"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
)

func TestFormalLocalStateSurvivesSoftwareUpdate(t *testing.T) {
    original := wire.Version
    defer func() { wire.Version = original }()
    wire.Version = "v1.0.0"
    path := filepath.Join(t.TempDir(), "stable-v1", "state.db")
    database, err := Open(context.Background(), path, true)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = database.Exec("INSERT INTO projects VALUES('root','p_original','lab'); INSERT INTO cloud_requests VALUES('request','2026-10-31', '{\"instance\":\"original\"}')"); err != nil {
        t.Fatal(err)
    }
    database.Close()
    wire.Version = "v1.1.0"
    database, err = Open(context.Background(), path, true)
    if err != nil {
        t.Fatal("new release rejected the original formal store", err)
    }
    defer database.Close()
    var id, receipt, series string
    if err = database.QueryRow("SELECT id FROM projects WHERE root='root'").Scan(&id); err != nil || id != "p_original" {
        t.Fatal(id, err)
    }
    if err = database.QueryRow("SELECT data FROM cloud_requests WHERE id='request'").Scan(&receipt); err != nil || receipt != `{"instance":"original"}` {
        t.Fatal(receipt, err)
    }
    if err = database.QueryRow("SELECT series FROM compatibility").Scan(&series); err != nil || series != "stable-v1" {
        t.Fatal(series, err)
    }
}

func TestMovedDevelopmentAndUnknownLocalStateAreNotAdopted(t *testing.T) {
    original := wire.Version
    defer func() { wire.Version = original }()
    wire.Version = "v1.0.0-rc.1"
    path := filepath.Join(t.TempDir(), "private", "state.db")
    database, err := Open(context.Background(), path, true)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = database.Exec("INSERT INTO projects VALUES('development-root','p_dev','lab')"); err != nil {
        t.Fatal(err)
    }
    database.Close()
    wire.Version = "v1.0.0"
    for _, write := range []bool{false, true} {
        if database, err = Open(context.Background(), path, write); err == nil {
            database.Close()
            t.Fatal("formal build adopted development state", write)
        }
    }
    database, err = sql.Open("sqlite", path)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    var series, id string
    if err = database.QueryRow("SELECT series FROM compatibility").Scan(&series); err != nil || series != "development" {
        t.Fatal("refusal rewrote development metadata", series, err)
    }
    if err = database.QueryRow("SELECT id FROM projects").Scan(&id); err != nil || id != "p_dev" {
        t.Fatal("refusal changed original project", id, err)
    }
    if _, err = database.Exec("PRAGMA user_version=3"); err != nil {
        t.Fatal(err)
    }
    if opened, err := Open(context.Background(), path, true); err == nil {
        opened.Close()
        t.Fatal("historical development schema accepted")
    }
    var schema int
    if err = database.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != 3 {
        t.Fatal("unknown schema was changed", schema, err)
    }
}
