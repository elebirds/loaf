package config

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

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
