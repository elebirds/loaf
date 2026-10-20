package config

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestStopGracePrecedenceAndExplicitZero(t *testing.T) {
    unset, err := ResolveStopGrace()
    if err != nil || unset != nil {
        t.Fatal(unset, err)
    }
    for _, tc := range []struct {
        layers []StopSettings
        want   time.Duration
    }{
        {[]StopSettings{{Grace: "30s"}, {}, {}}, 30 * time.Second},
        {[]StopSettings{{Grace: "30s"}, {Grace: "2s"}, {}}, 2 * time.Second},
        {[]StopSettings{{Grace: "30s"}, {Grace: "2s"}, {Grace: "0s"}}, 0},
    } {
        got, err := ResolveStopGrace(tc.layers...)
        if err != nil || got == nil || *got != tc.want {
            t.Fatal(got, err, tc.want)
        }
    }
    for _, bad := range []string{"-1s", "5m1ms", "bad"} {
        if _, err := ResolveStopGrace(StopSettings{Grace: bad}); err == nil {
            t.Fatal("accepted invalid grace", bad)
        }
    }
}

func TestStopSettingsPersistWithMachineUpdates(t *testing.T) {
    store := testStore(t)
    if err := os.MkdirAll(filepath.Dir(store.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(store.ConfigFile, []byte("format_version = 1\n[stop]\ngrace = '0s'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := store.Add(context.Background(), "lab", Target{Destination: "lab", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    cfg, err := store.Load()
    if err != nil || cfg.Stop.Grace != "0s" {
        t.Fatal(cfg, err)
    }
    root := t.TempDir()
    file := filepath.Join(root, "loaf.toml")
    if err := os.WriteFile(file, []byte("[stop]\ngrace = '2s'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    _, project, err := Discover(context.Background(), root)
    if err != nil || project.Stop.Grace != "2s" {
        t.Fatal(project, err)
    }
    for _, data := range []string{"[stop]\ngrace = -1", "[stop]\ngrace = 'bad'", "[stop]\nunknown = true"} {
        if err := os.WriteFile(file, []byte(data), 0600); err != nil {
            t.Fatal(err)
        }
        if _, _, err := Discover(context.Background(), root); err == nil {
            t.Fatal("accepted invalid stop configuration", data)
        }
    }
}
