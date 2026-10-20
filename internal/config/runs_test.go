package config

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "testing"
    "time"
)

func reference(n int) RunRef {
    now := time.Now().UTC()
    return RunRef{ID: fmt.Sprintf("r_%024x", n), ProjectID: "p_000000000000000000000001", Target: "lab", Connection: Target{Destination: "lab", RemoteBase: "~/projects"}, RemoteDir: "/tmp/project", Runner: "/tmp/runner", Version: "dev", CreatedAt: now, Submission: "preparing"}
}

func TestRunLocatorsAreLockedAndDoNotPromotePreparation(t *testing.T) {
    store := testStore(t)
    var wg sync.WaitGroup
    for n := 0; n < 12; n++ {
        wg.Add(1)
        go func(n int) {
            defer wg.Done()
            ref := reference(n)
            if n != 0 {
                ref.Submission = "uncertain"
                ref.AttemptedAt = &ref.CreatedAt
            }
            if err := store.SaveRun(context.Background(), ref); err != nil {
                t.Error(err)
            }
        }(n)
    }
    wg.Wait()
    refs, err := store.Runs(reference(0).ProjectID)
    if err != nil || len(refs) != 11 {
        t.Fatal(len(refs), err)
    }
    other, err := store.Runs("p_000000000000000000000002")
    if err != nil || len(other) != 0 {
        t.Fatal(other, err)
    }
    original := `{"format_version":99,"runs":{}}`
    if err := os.WriteFile(store.runsFile(), []byte(original), 0600); err != nil {
        t.Fatal(err)
    }
    if store.SaveRun(context.Background(), reference(99)) == nil {
        t.Fatal("overwrote unknown format")
    }
    data, _ := os.ReadFile(store.runsFile())
    if string(data) != original {
        t.Fatal(string(data))
    }
}

func TestExecutionPreferencesAndProjectOnlyCommands(t *testing.T) {
    wait, err := ResolveWait("10s", "0s")
    if err != nil || wait != 0 {
        t.Fatal(wait, err)
    }
    zero := 0
    tail, err := ResolveTail(LogSettings{&zero})
    if err != nil || tail != 0 {
        t.Fatal(tail, err)
    }
    for _, value := range []string{"-1s", "6m", "no"} {
        if _, err := ResolveWait(value); err == nil {
            t.Fatal(value)
        }
    }
    store := testStore(t)
    os.MkdirAll(filepath.Dir(store.ConfigFile), 0700)
    if err := os.WriteFile(store.ConfigFile, []byte("format_version = 1\n[run]\nwait = '10s'\n[logs]\ntail = 20\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := store.Add(context.Background(), "lab", Target{Destination: "lab", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    cfg, err := store.Load()
    if err != nil || cfg.Run.Wait != "10s" || cfg.Logs.Tail == nil || *cfg.Logs.Tail != 20 {
        t.Fatal(cfg, err)
    }
    os.WriteFile(store.ConfigFile, []byte("format_version = 1\n[run]\ncommand = 'unsafe global command'\n"), 0600)
    if _, err := store.Load(); err == nil {
        t.Fatal("accepted global command")
    }
    root := t.TempDir()
    os.WriteFile(filepath.Join(root, "loaf.toml"), []byte("[run]\ncommand = 'python train.py'\nsetup = 'uv sync'\nwait = '0s'\n[run.env]\nEXPERIMENT = 'baseline'\n[logs]\ntail = 0\n"), 0600)
    _, project, err := Discover(context.Background(), root)
    if err != nil || project.Run.Env["EXPERIMENT"] != "baseline" || project.Run.Wait != "0s" {
        t.Fatal(project, err)
    }
}
