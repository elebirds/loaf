package config

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestPullPreferencesAndRoundTrip(t *testing.T) {
    yes, no := true, false
    policy, err := ResolvePull(PullSettings{BaseDir: "collected", Checksum: &yes}, PullSettings{Checksum: &no})
    if err != nil || policy.BaseDir != "collected" || policy.Checksum {
        t.Fatal(policy, err)
    }
    defaults, err := ResolvePull()
    if err != nil || defaults.BaseDir != "loaf-results" || defaults.Checksum {
        t.Fatal(defaults, err)
    }
    if _, err := ResolvePull(PullSettings{BaseDir: "bad\x00name"}); err == nil {
        t.Fatal("accepted invalid local path")
    }
    store := testStore(t)
    if err := os.MkdirAll(filepath.Dir(store.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(store.ConfigFile, []byte("format_version=1\n[pull]\nbase_dir='collected'\nchecksum=false\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := store.Add(context.Background(), "lab", Target{Destination: "lab", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    cfg, err := store.Load()
    if err != nil || cfg.Pull.BaseDir != "collected" || cfg.Pull.Checksum == nil || *cfg.Pull.Checksum {
        t.Fatal(cfg, err)
    }
    project := t.TempDir()
    if err := os.WriteFile(filepath.Join(project, "loaf.toml"), []byte("[pull]\nchecksum=true\n"), 0600); err != nil {
        t.Fatal(err)
    }
    _, file, err := Discover(context.Background(), project)
    if err != nil || file.Pull.Checksum == nil || !*file.Pull.Checksum {
        t.Fatal(file, err)
    }
    for _, text := range []string{"[pull]\nchecksum='false'", "[pull]\nunknown=true", "[pull]\nbase_dir=1"} {
        if err := os.WriteFile(filepath.Join(project, "loaf.toml"), []byte(text), 0600); err != nil {
            t.Fatal(err)
        }
        if _, _, err := Discover(context.Background(), project); err == nil {
            t.Fatal("accepted invalid pull settings", text)
        }
    }
}

func TestPullBaseIsProjectRelativeAndSeparate(t *testing.T) {
    project := filepath.Join(t.TempDir(), "project")
    for _, name := range []string{".", "..", project, filepath.Dir(project), "/"} {
        if _, err := PullBase(project, PullPolicy{BaseDir: name}); err == nil {
            t.Fatal("accepted a result root containing source", name)
        }
    }
    for _, name := range []string{"results", filepath.Join(filepath.Dir(project), "results")} {
        base, err := PullBase(project, PullPolicy{BaseDir: name})
        if err != nil || !filepath.IsAbs(base) {
            t.Fatal(base, err)
        }
    }
}
