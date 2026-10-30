package config

import (
    "context"
    "github.com/elebirds/loaf/internal/testutil"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func testStore(t *testing.T) *Store { t.Helper(); return New(t.TempDir(), "", "") }

func TestSelectionsAndPrecedence(t *testing.T) {
    s := testStore(t)
    ctx := context.Background()
    for _, name := range []string{"a", "b", "c"} {
        if err := s.Add(ctx, name, Target{Destination: name, RemoteBase: "~/projects"}, false); err != nil {
            t.Fatal(err)
        }
    }
    cfg, err := s.Load()
    if err != nil {
        t.Fatal(err)
    }
    for _, tc := range []struct{ explicit, personal, file, want string }{{"c", "a", "b", "c"}, {"", "a", "b", "a"}, {"", "", "b", "b"}} {
        file := ProjectFile{Target: tc.file}
        name, _, err := Resolve(tc.explicit, Project{Target: tc.personal}, file, cfg)
        if err != nil || name != tc.want {
            t.Fatalf("%+v: %q %v", tc, name, err)
        }
    }
    if _, _, err := Resolve("", Project{}, ProjectFile{}, cfg); err == nil {
        t.Fatal("inherited a different project's target")
    }
    if err := s.Add(ctx, "a", Target{Destination: "new", RemoteBase: "~/new"}, false); err == nil {
        t.Fatal("overwrote alias")
    }
    if err := s.Add(ctx, "a", Target{Destination: "new", RemoteBase: "~/new"}, true); err != nil {
        t.Fatal(err)
    }
    testutil.Private(t, s.ConfigFile, false)
}

func TestDiscovery(t *testing.T) {
    root := t.TempDir()
    child := filepath.Join(root, "space 中文", "child")
    if err := os.MkdirAll(child, 0700); err != nil {
        t.Fatal(err)
    }
    if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
        t.Fatal(err)
    }
    got, _, err := Discover(context.Background(), child)
    realRoot, _ := filepath.EvalSymlinks(root)
    if err != nil || got != realRoot {
        t.Fatalf("git root %q %v", got, err)
    }
    configRoot := filepath.Dir(child)
    if err := os.WriteFile(filepath.Join(configRoot, "loaf.toml"), []byte("target = 'demo'\n[run]\ncommand='python train.py'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    got, c, err := Discover(context.Background(), child)
    realRoot, _ = filepath.EvalSymlinks(configRoot)
    if err != nil || got != realRoot || c.Target != "demo" {
        t.Fatalf("toml root %q %+v %v", got, c, err)
    }
}

func TestRemotePaths(t *testing.T) {
    for _, s := range []string{"/", "///", "/.", "~/", "~//", "~/../other", "/tmp/../etc", "relative", "/tmp\nother"} {
        if ValidRemotePath(s) {
            t.Errorf("accepted %q", s)
        }
    }
    for _, s := range []string{"~/projects", "/tmp/a space/中文"} {
        if !ValidRemotePath(s) {
            t.Errorf("rejected %q", s)
        }
    }
}
