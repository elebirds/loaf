package local

import (
    "context"
    "database/sql"
    "path/filepath"
    "sync"
    "testing"

    "github.com/elebirds/loaf/internal/config"
)

func testStore(t *testing.T) *Store { t.Helper(); return New(t.TempDir()) }

func TestProjectIdentityChoiceAndRemovedAlias(t *testing.T) {
    s := testStore(t)
    ctx := context.Background()
    project, err := s.Select(ctx, t.TempDir(), "old")
    if err != nil {
        t.Fatal(err)
    }
    again, err := s.Select(ctx, project.Root, "")
    if err != nil || again != project {
        t.Fatal(again, err)
    }
    a, err := s.Select(ctx, filepath.Join(t.TempDir(), "same"), "")
    if err != nil {
        t.Fatal(err)
    }
    b, err := s.Select(ctx, filepath.Join(t.TempDir(), "same"), "")
    if err != nil || a.ID == b.ID {
        t.Fatal(a, b, err)
    }
    settings := config.New(t.TempDir(), "", "")
    if err := settings.Add(ctx, "old", config.Target{Destination: "host", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    if err := settings.Remove(ctx, "old"); err != nil {
        t.Fatal(err)
    }
    kept, err := s.Existing(project.Root)
    if err != nil || kept != project {
        t.Fatal(kept, err)
    }
    cfg, err := settings.Load()
    if err != nil {
        t.Fatal(err)
    }
    if _, _, err := config.Resolve("", kept, config.ProjectFile{Target: "new"}, cfg); err == nil {
        t.Fatal("removed alias silently changed project choice")
    }
}

func TestConcurrentProjectsAndUnknownSchema(t *testing.T) {
    s := testStore(t)
    roots := make([]string, 20)
    for i := range roots {
        roots[i] = filepath.Join(t.TempDir(), "project")
    }
    var wg sync.WaitGroup
    for _, root := range roots {
        wg.Go(func() {
            if _, err := s.Select(context.Background(), root, ""); err != nil {
                t.Error(err)
            }
        })
    }
    wg.Wait()
    ids := map[string]bool{}
    for _, root := range roots {
        p, err := s.Existing(root)
        if err != nil || p.Root != root || p.ID == "" || ids[p.ID] {
            t.Fatal(p, err)
        }
        ids[p.ID] = true
    }
    database, err := sql.Open("sqlite", s.Path)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := database.Exec("PRAGMA user_version=99"); err != nil {
        t.Fatal(err)
    }
    database.Close()
    if _, err := s.Select(context.Background(), t.TempDir(), ""); err == nil {
        t.Fatal("accepted unknown schema")
    }
    if _, err := s.Existing(roots[0]); err == nil {
        t.Fatal("overwrote unknown schema")
    }
}
