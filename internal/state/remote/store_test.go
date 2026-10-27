package remotestate

import (
    "database/sql"
    "os"
    "path/filepath"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/wire"
)

func fixture(t *testing.T) (string, *wire.RunRecord) {
    t.Helper()
    root := t.TempDir()
    if err := Initialize(root, "project"); err != nil {
        t.Fatal(err)
    }
    return root, &wire.RunRecord{FormatVersion: wire.FormatVersion, Request: wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Root: root, ProjectID: "project", RunID: "run", Env: map[string]string{"TOKEN": "private"}}, Status: "starting", Phase: "copying", CreatedAt: time.Now().UTC()}
}

func TestPrivateReadOnlyAndUnknownSchema(t *testing.T) {
    root, _ := fixture(t)
    info, err := os.Stat(Path(root))
    if err != nil || info.Mode().Perm() != 0600 {
        t.Fatal(info, err)
    }
    before, err := os.ReadDir(filepath.Join(root, Namespace))
    if err != nil {
        t.Fatal(err)
    }
    store, err := Open(root, false)
    if err != nil {
        t.Fatal(err)
    }
    if err := store.VerifyProject("project"); err != nil {
        t.Fatal(err)
    }
    if err := store.VerifyProject("other"); err == nil {
        t.Fatal("accepted foreign ownership")
    }
    if err := store.SetSync(true); err == nil {
        t.Fatal("writable read connection")
    }
    store.Close()
    after, _ := os.ReadDir(filepath.Join(root, Namespace))
    if len(before) != len(after) {
        t.Fatalf("read created sidecars: before=%v after=%v", before, after)
    }
    missing := filepath.Join(t.TempDir(), "missing")
    if _, err := Open(missing, true); err == nil {
        t.Fatal("opened missing project")
    }
    if _, err := os.Stat(missing); !os.IsNotExist(err) {
        t.Fatal("created project", err)
    }
    db, err := sql.Open("sqlite", Path(root))
    if err != nil {
        t.Fatal(err)
    }
    if _, err := db.Exec("PRAGMA user_version=99"); err != nil {
        t.Fatal(err)
    }
    db.Close()
    if _, err := Open(root, true); err == nil || err.Error() != "BAD_FORMAT" {
        t.Fatal(err)
    }
}

func TestClaimTerminalAndCleanupAreDurable(t *testing.T) {
    root, r := fixture(t)
    s, err := Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    if err := s.CreateRun(r); err != nil {
        t.Fatal(err)
    }
    s.Close()
    s, err = Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    if err := s.CreateRun(r); err == nil || err.Error() != "RUN_EXISTS" {
        t.Fatal("reused claim", err)
    }
    if err := s.AcceptStop(r.Request.RunID, 50); err != nil {
        t.Fatal(err)
    }
    if err := s.AcceptStop(r.Request.RunID, 0); err != nil {
        t.Fatal(err)
    }
    grace, err := s.Stop(r.Request.RunID)
    if err != nil || grace == nil || *grace != 50 {
        t.Fatal(grace, err)
    }
    now := time.Now().UTC()
    r.Status = "succeeded"
    r.CompletedAt = &now
    r.ExitCode = new(0)
    if err := s.SaveRun(r); err != nil {
        t.Fatal(err)
    }
    r.Status = "running"
    if err := s.SaveRun(r); err == nil {
        t.Fatal("regressed terminal")
    }
    retained, err := s.Run(r.Request.RunID)
    if err != nil || retained.Status != "succeeded" {
        t.Fatal(retained, err)
    }
    receipt := &wire.CleanupReceipt{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: r.Request.RunID, Summary: wire.Summary(retained), Device: 1, Inode: 2}
    if err := s.SaveCleanup(receipt); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Run(r.Request.RunID); err == nil || err.Error() != "RUN_NOT_FOUND" {
        t.Fatal(err)
    }
    if stop, err := s.Stop(r.Request.RunID); err != nil || stop != nil {
        t.Fatal(stop, err)
    }
    if err := s.CreateRun(r); err == nil || err.Error() != "RUN_EXISTS" {
        t.Fatal("cleanup released claim", err)
    }
    receipt.CleanedAt = &now
    if err := s.SaveCleanup(receipt); err != nil {
        t.Fatal(err)
    }
    got, err := s.Cleanup(r.Request.RunID)
    if err != nil || got.CleanedAt == nil || got.Summary.Status != "succeeded" {
        t.Fatal(got, err)
    }
}

func TestConcurrentClaimsHaveOneWinner(t *testing.T) {
    root, r := fixture(t)
    var wg sync.WaitGroup
    results := make(chan error, 8)
    for range 8 {
        wg.Go(func() {
            s, err := Open(root, true)
            if err == nil {
                err = s.CreateRun(r)
                s.Close()
            }
            results <- err
        })
    }
    wg.Wait()
    close(results)
    winners := 0
    for err := range results {
        if err == nil {
            winners++
        } else if err.Error() != "RUN_EXISTS" {
            t.Fatal(err)
        }
    }
    if winners != 1 {
        t.Fatal(winners)
    }
}
