package config

import (
    "path/filepath"
    "strings"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/localpath"
)

// PullSettings contains independent user/project download preferences.
type PullSettings struct {
    BaseDir  string `toml:"base_dir,omitempty"`
    Checksum *bool  `toml:"checksum,omitempty"`
}

// PullPolicy controls the result root and native rsync change detection.
type PullPolicy struct {
    BaseDir  string `json:"base_dir"`
    Checksum bool   `json:"checksum"`
}

// ResolvePull preserves unset and explicit false values across scopes.
func ResolvePull(layers ...PullSettings) (PullPolicy, error) {
    policy := PullPolicy{BaseDir: "loaf-results"}
    for _, layer := range layers {
        if layer.BaseDir != "" {
            if strings.ContainsAny(layer.BaseDir, "\x00\r\n") {
                return policy, fault.New("BAD_CONFIG", "pull_base_dir", nil)
            }
            policy.BaseDir = layer.BaseDir
        }
        if layer.Checksum != nil {
            policy.Checksum = *layer.Checksum
        }
    }
    return policy, nil
}

// PullBase resolves relative preferences against the project, never the caller's
// subdirectory. A result root must not contain the project's source tree.
func PullBase(project string, policy PullPolicy) (string, error) {
    base := policy.BaseDir
    if !filepath.IsAbs(base) {
        base = filepath.Join(project, base)
    }
    base = filepath.Clean(base)
    if err := localpath.Supported(base); err != nil {
        return "", fault.Wrap("BAD_CONFIG", "pull_base_dir", err)
    }
    if localpath.Contains(base, project) {
        return "", fault.New("BAD_CONFIG", "pull_base_dir", nil)
    }
    return base, nil
}
