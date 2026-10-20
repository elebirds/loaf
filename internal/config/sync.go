package config

import (
    "strings"

    "github.com/elebirds/loaf/internal/fault"
)

// SyncSettings holds optional overrides. A pointer distinguishes false from unset.
type SyncSettings struct {
    Mode             string   `toml:"mode,omitempty"`
    RespectGitignore *bool    `toml:"respect_gitignore,omitempty"`
    Exclude          []string `toml:"exclude,omitempty"`
}

// SyncPolicy is the effective strategy, independent of transfer implementation.
type SyncPolicy struct {
    Mode             string   `json:"mode"`
    RespectGitignore bool     `json:"respect_gitignore"`
    Exclude          []string `json:"exclude"`
}

// ResolveSync applies global, project, and explicit overrides in that order.
// Scalars override individually; exclusions append without weakening defaults.
func ResolveSync(layers ...SyncSettings) (SyncPolicy, error) {
    result := SyncPolicy{Mode: "mirror", RespectGitignore: true, Exclude: []string{}}
    for _, layer := range layers {
        if layer.Mode != "" {
            if layer.Mode != "mirror" && layer.Mode != "update" {
                return result, fault.New("BAD_CONFIG", "sync_mode", nil)
            }
            result.Mode = layer.Mode
        }
        if layer.RespectGitignore != nil {
            result.RespectGitignore = *layer.RespectGitignore
        }
        for _, pattern := range layer.Exclude {
            if pattern == "" || strings.ContainsAny(pattern, "\x00\r\n") || strings.HasPrefix(pattern, "!") {
                return result, fault.New("BAD_CONFIG", "sync_exclude", nil)
            }
            result.Exclude = append(result.Exclude, pattern)
        }
    }
    return result, nil
}
