package config

import (
    "os"
    "path/filepath"
)

func defaultDirectories(home string) (string, string) {
    return filepath.Join(home, "AppData", "Roaming"), filepath.Join(home, "AppData", "Local")
}

func userDirectories(home string) (string, string) {
    config, state := defaultDirectories(home)
    if value := os.Getenv("APPDATA"); value != "" {
        config = value
    }
    if value := os.Getenv("LOCALAPPDATA"); value != "" {
        state = value
    }
    return config, state
}
