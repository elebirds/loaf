package config

import (
    "strings"

    "github.com/elebirds/loaf/internal/fault"
)

// ExecSettings contains foreground-operation preferences. An explicit empty CWD
// restores the remote user's home rather than inheriting another scope's value.
type ExecSettings struct {
    CWD *string `toml:"cwd,omitempty"`
}

// ResolveExecCWD merges user, project, and explicit settings in that order.
// Relative directories are resolved from the remote user's home, never cache/.
func ResolveExecCWD(layers ...ExecSettings) (string, error) {
    directory := ""
    for _, layer := range layers {
        if layer.CWD != nil {
            directory = *layer.CWD
        }
    }
    if strings.ContainsRune(directory, 0) {
        return "", fault.New("BAD_CONFIG", "exec_directory", nil)
    }
    return directory, nil
}
