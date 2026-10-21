package config

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestPSLimitPrecedenceAndExplicitZero(t *testing.T) {
    one, three, zero, high := 1, 3, 0, 1001
    for _, tc := range []struct {
        layers []PSSettings
        want   int
    }{
        {nil, 20},
        {[]PSSettings{{Limit: &three}, {}}, 3},
        {[]PSSettings{{Limit: &three}, {Limit: &one}, {}}, 1},
        {[]PSSettings{{Limit: &one}, {Limit: &three}}, 3},
    } {
        got, err := ResolvePSLimit(tc.layers...)
        if err != nil || got != tc.want {
            t.Fatal(got, err, tc.want)
        }
    }
    for _, bad := range []int{zero, -1, high} {
        if _, err := ResolvePSLimit(PSSettings{Limit: &bad}); err == nil {
            t.Fatal("accepted invalid limit", bad)
        }
    }
}

func TestPSSettingsPersistAndValidate(t *testing.T) {
    store := testStore(t)
    if err := os.MkdirAll(filepath.Dir(store.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(store.ConfigFile, []byte("format_version = 1\n[ps]\nlimit = 37\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := store.Add(context.Background(), "lab", Target{Destination: "lab", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    if err := store.Remove(context.Background(), "lab"); err != nil {
        t.Fatal(err)
    }
    cfg, err := store.Load()
    if err != nil || cfg.PS.Limit == nil || *cfg.PS.Limit != 37 {
        t.Fatal(cfg, err)
    }
    root := t.TempDir()
    file := filepath.Join(root, "loaf.toml")
    if err := os.WriteFile(file, []byte("[ps]\nlimit = 5\n"), 0600); err != nil {
        t.Fatal(err)
    }
    _, project, err := Discover(context.Background(), root)
    if err != nil || project.PS.Limit == nil || *project.PS.Limit != 5 {
        t.Fatal(project, err)
    }
    for _, value := range []string{"0", "-1", "1001", "'20'"} {
        for _, destination := range []string{file, store.ConfigFile} {
            data := "[ps]\nlimit = " + value + "\n"
            if destination == store.ConfigFile {
                data = "format_version = 1\n" + data
            }
            if err := os.WriteFile(destination, []byte(data), 0600); err != nil {
                t.Fatal(err)
            }
            if destination == file {
                if _, _, err := Discover(context.Background(), root); err == nil {
                    t.Fatal("accepted invalid project limit", value)
                }
            } else if _, err := store.Load(); err == nil {
                t.Fatal("accepted invalid user limit", value)
            }
        }
    }
}
