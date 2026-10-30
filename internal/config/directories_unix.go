//go:build !windows

package config

import "path/filepath"

func defaultDirectories(home string) (string, string) {
    return filepath.Join(home, ".config"), filepath.Join(home, ".local", "state")
}

func userDirectories(home string) (string, string) { return defaultDirectories(home) }
