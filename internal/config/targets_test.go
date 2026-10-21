package config

import (
    "context"
    "os"
    "sync"
    "testing"

    "github.com/elebirds/loaf/internal/atomicfile"
)

func TestRemovePreservesProjectChoiceAndConcurrentChanges(t *testing.T) {
    store := testStore(t)
    ctx := context.Background()
    target := Target{Destination: "old", RemoteBase: "~/projects"}
    if err := store.Add(ctx, "old", target, false); err != nil {
        t.Fatal(err)
    }
    project, err := store.Select(ctx, t.TempDir(), "old")
    if err != nil {
        t.Fatal(err)
    }
    var group sync.WaitGroup
    group.Add(2)
    go func() {
        defer group.Done()
        if err := store.Remove(ctx, "old"); err != nil {
            t.Error(err)
        }
    }()
    go func() {
        defer group.Done()
        if err := store.Add(ctx, "new", target, false); err != nil {
            t.Error(err)
        }
    }()
    group.Wait()
    cfg, err := store.Load()
    if err != nil || len(cfg.Targets) != 1 || cfg.Targets["new"].Destination != "old" {
        t.Fatal(cfg, err)
    }
    kept, err := store.Existing(project.Root)
    if err != nil || kept != project {
        t.Fatal(kept, err)
    }
    if _, _, err := Resolve("", kept, ProjectFile{Target: "new"}, cfg); err == nil {
        t.Fatal("removed personal choice fell back to shared target")
    }
    if err := store.Remove(ctx, "old"); err == nil {
        t.Fatal("unknown alias removed")
    }
    if err := store.Remove(ctx, "../new"); err == nil {
        t.Fatal("invalid alias accepted")
    }
}

func TestRemoveDoesNotOverwriteUnknownConfig(t *testing.T) {
    store := testStore(t)
    original := []byte("format_version = 99\n")
    if err := atomicfile.Write(store.ConfigFile, original); err != nil {
        t.Fatal(err)
    }
    if err := store.Remove(context.Background(), "old"); err == nil {
        t.Fatal("unknown configuration accepted")
    }
    after, err := os.ReadFile(store.ConfigFile)
    if err != nil || string(after) != string(original) {
        t.Fatal(string(after), err)
    }
}
