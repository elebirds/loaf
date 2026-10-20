// Package config reads user-owned configuration from private directories.
package config

import (
    "bytes"
    "errors"
    "os"
    "path/filepath"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/pelletier/go-toml/v2"
)

// Target is an existing SSH connection, not a provisioned machine.
type Target struct {
    Destination string   `toml:"destination" json:"destination"`
    Args        []string `toml:"args,omitempty" json:"args,omitempty"`
    RemoteBase  string   `toml:"remote_base" json:"remote_base"`
}

// Global is the user-owned configuration, independent of project TOML.
type Global struct {
    FormatVersion int               `toml:"format_version"`
    Language      string            `toml:"language,omitempty"`
    Targets       map[string]Target `toml:"targets"`
}

// Store identifies private user configuration and state paths.
type Store struct{ ConfigFile, IndexFile string }

// New uses XDG directory overrides, or their documented home-relative defaults.
func New(home, configHome, stateHome string) *Store {
    if configHome == "" {
        configHome = filepath.Join(home, ".config")
    }
    if stateHome == "" {
        stateHome = filepath.Join(home, ".local", "state")
    }
    return &Store{filepath.Join(configHome, "loaf", "config.toml"), filepath.Join(stateHome, "loaf", "index.json")}
}

func decodeTOML(data []byte, dst any) error {
    return toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(dst)
}

// Load reads configuration without creating files. Unknown formats are preserved.
func (s *Store) Load() (Global, error) {
    c := Global{FormatVersion: 1, Targets: map[string]Target{}}
    data, err := os.ReadFile(s.ConfigFile)
    if errors.Is(err, os.ErrNotExist) {
        return c, nil
    }
    if err != nil {
        return c, fault.Wrap("BAD_CONFIG", "config_read", err)
    }
    if err := decodeTOML(data, &c); err != nil {
        return c, fault.Wrap("BAD_CONFIG", "config_read", err)
    }
    if c.FormatVersion != 1 {
        return c, fault.New("BAD_CONFIG", "unknown_format", nil)
    }
    if c.Targets == nil {
        c.Targets = map[string]Target{}
    }
    return c, nil
}
