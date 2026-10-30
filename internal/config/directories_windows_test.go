package config

import (
    "path/filepath"
    "testing"
)

func TestWindowsUserDirectoriesAndExplicitXDGOverrides(t *testing.T) {
    roaming, local := t.TempDir(), t.TempDir()
    t.Setenv("APPDATA", roaming)
    t.Setenv("LOCALAPPDATA", local)
    t.Setenv("XDG_CONFIG_HOME", "")
    t.Setenv("XDG_STATE_HOME", "")
    store := NewUser(t.TempDir())
    if store.ConfigFile != filepath.Join(roaming, "loaf", "config.toml") || store.StateDir != filepath.Join(local, "loaf") {
        t.Fatal(store)
    }
    xdg := t.TempDir()
    t.Setenv("XDG_CONFIG_HOME", xdg)
    t.Setenv("XDG_STATE_HOME", xdg)
    store = NewUser(t.TempDir())
    if store.ConfigFile != filepath.Join(xdg, "loaf", "config.toml") || store.StateDir != filepath.Join(xdg, "loaf") {
        t.Fatal(store)
    }
}
