package config

import (
    "context"
    "github.com/elebirds/loaf/internal/atomicfile"
    "os"
    "os/exec"
    "path/filepath"
    "sync"
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
    root := t.TempDir()
    p, err := s.Select(ctx, root, "a")
    if err != nil {
        t.Fatal(err)
    }
    again, err := s.Select(ctx, root, "")
    if err != nil || again.ID != p.ID || again.Target != "a" {
        t.Fatalf("selection: %+v %v", again, err)
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
    mode, _ := os.Stat(s.ConfigFile)
    if mode.Mode().Perm() != 0600 {
        t.Fatal(mode.Mode())
    }
}

func TestConcurrentProjectsAndUnknownFormats(t *testing.T) {
    s := testStore(t)
    var wg sync.WaitGroup
    roots := make([]string, 20)
    for n := range roots {
        roots[n] = t.TempDir()
    }
    for _, root := range roots {
        wg.Add(1)
        go func() {
            defer wg.Done()
            if _, err := s.Select(context.Background(), root, ""); err != nil {
                t.Error(err)
            }
        }()
    }
    wg.Wait()
    state, err := s.loadIndex()
    if err != nil || len(state.Projects) != 20 {
        t.Fatalf("lost updates: %v %d", err, len(state.Projects))
    }
    original := []byte(`{"format_version":99,"projects":{}}`)
    if err := os.WriteFile(s.IndexFile, original, 0600); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Select(context.Background(), filepath.Join(t.TempDir(), "new"), ""); err == nil {
        t.Fatal("accepted unknown format")
    }
    after, _ := os.ReadFile(s.IndexFile)
    if string(after) != string(original) {
        t.Fatal("overwrote unknown format")
    }
    if err := atomicfile.Write(s.ConfigFile, []byte("format_version = 99\n")); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Load(); err == nil {
        t.Fatal("accepted unknown config")
    }
    for _, original := range []string{`{"format_version":1,"projects":{},"future":true}`, `{"format_version":1,"projects":{"/project":{"root":"/project","id":"../other"}}}`} {
        if err := os.WriteFile(s.IndexFile, []byte(original), 0600); err != nil {
            t.Fatal(err)
        }
        if _, err := s.Select(context.Background(), "/project", ""); err == nil {
            t.Fatal("accepted unsafe or unknown state")
        }
        after, _ := os.ReadFile(s.IndexFile)
        if string(after) != original {
            t.Fatal("changed invalid state")
        }
    }
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
    s := testStore(t)
    a, _ := s.Select(context.Background(), "/a/same", "")
    b, _ := s.Select(context.Background(), "/b/same", "")
    if a.ID == b.ID {
        t.Fatal("basename collision")
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
