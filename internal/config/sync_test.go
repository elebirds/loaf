package config

import (
    "context"
    "os"
    "path/filepath"
    "reflect"
    "testing"
)

func TestSyncSettingsMergeIndividualFieldsAndAppendExclusions(t *testing.T) {
    no, yes := false, true
    global := SyncSettings{Mode: "update", RespectGitignore: &no, Exclude: []string{"global/"}}
    project := SyncSettings{Mode: "mirror", Exclude: []string{"project/"}}
    cli := SyncSettings{RespectGitignore: &yes, Exclude: []string{"command/"}}
    policy, err := ResolveSync(global, project, cli)
    want := SyncPolicy{"mirror", true, []string{"global/", "project/", "command/"}}
    if err != nil || !reflect.DeepEqual(policy, want) || len(global.Exclude) != 1 {
        t.Fatal(policy, err)
    }
    policy, err = ResolveSync()
    if err != nil || policy.Mode != "mirror" || !policy.RespectGitignore {
        t.Fatal(policy, err)
    }
    policy, err = ResolveSync(global, project)
    if err != nil || policy.RespectGitignore || policy.Mode != "mirror" {
        t.Fatal("unset project field masked a global default", policy, err)
    }
    for _, layer := range []SyncSettings{{Mode: "typo"}, {Exclude: []string{"!.env"}}, {Exclude: []string{"a\nb"}}, {Exclude: []string{""}}} {
        if _, err := ResolveSync(layer); err == nil {
            t.Fatal("accepted invalid settings", layer)
        }
    }
}

func TestSyncSettingsRoundTripWithMachineUpdates(t *testing.T) {
    store := testStore(t)
    if err := os.MkdirAll(filepath.Dir(store.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    text := "format_version = 1\nlanguage = 'zh-Hans'\n[sync]\nmode = 'update'\nrespect_gitignore = false\nexclude = ['scratch/']\n"
    if err := os.WriteFile(store.ConfigFile, []byte(text), 0600); err != nil {
        t.Fatal(err)
    }
    if err := store.Add(context.Background(), "lab", Target{Destination: "lab", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    cfg, err := store.Load()
    if err != nil || cfg.Sync.Mode != "update" || cfg.Sync.RespectGitignore == nil || *cfg.Sync.RespectGitignore || !reflect.DeepEqual(cfg.Sync.Exclude, []string{"scratch/"}) {
        t.Fatal(cfg, err)
    }
    root := t.TempDir()
    if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte("[sync]\nrespect_gitignore = false\n"), 0600); err != nil {
        t.Fatal(err)
    }
    _, project, err := Discover(context.Background(), root)
    if err != nil || project.Sync.RespectGitignore == nil || *project.Sync.RespectGitignore {
        t.Fatal(project, err)
    }
    for _, text := range []string{"[sync]\nmode = 'typo'", "[sync]\nrespect_gitignore = 'no'", "[sync]\nunknown = true"} {
        if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte(text), 0600); err != nil {
            t.Fatal(err)
        }
        if _, _, err := Discover(context.Background(), root); err == nil {
            t.Fatal("accepted invalid project settings", text)
        }
    }
}
