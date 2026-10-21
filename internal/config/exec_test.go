package config

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestExecDirectoryPrecedenceAndExplicitHome(t *testing.T) {
    value := func(s string) ExecSettings { return ExecSettings{CWD: &s} }
    for _, tc := range []struct {
        settings []ExecSettings
        want     string
    }{
        {nil, ""},
        {[]ExecSettings{value("/user"), {}}, "/user"},
        {[]ExecSettings{value("/user"), value("~/project")}, "~/project"},
        {[]ExecSettings{value("/user"), value("~/project"), value("/flag")}, "/flag"},
        {[]ExecSettings{value("/user"), value("")}, ""},
    } {
        got, err := ResolveExecCWD(tc.settings...)
        if err != nil || got != tc.want {
            t.Fatal(got, err, tc.want)
        }
    }
    if _, err := ResolveExecCWD(value("a\x00b")); err == nil {
        t.Fatal("accepted NUL")
    }
    project := t.TempDir()
    if err := os.WriteFile(filepath.Join(project, "loaf.toml"), []byte("[exec]\ncwd = ''\n"), 0600); err != nil {
        t.Fatal(err)
    }
    _, file, err := Discover(context.Background(), project)
    if err != nil || file.Exec.CWD == nil || *file.Exec.CWD != "" {
        t.Fatal(file, err)
    }
    store := New(t.TempDir(), "", "")
    if err := os.MkdirAll(filepath.Dir(store.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(store.ConfigFile, []byte("format_version = 1\n[exec]\ncwd = ''\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := store.Add(context.Background(), "demo", Target{Destination: "host", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    loaded, err := store.Load()
    if err != nil || loaded.Exec.CWD == nil || *loaded.Exec.CWD != "" {
        t.Fatal("alias save lost explicit home", loaded, err)
    }
}
