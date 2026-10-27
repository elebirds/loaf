package run

import (
    "encoding/json"
    "maps"
    "slices"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/wire"
)

// ExecutionSpec is a validated local execution value, not a source snapshot or
// a submission identity. Environment values remain private to the launch request.
type ExecutionSpec struct {
    Args    []string
    Setup   string
    Env     map[string]string
    Outputs []string
}

// ResolveExecution applies the command and output defaults shared by preview
// and submission without allocating identities or performing side effects.
func ResolveExecution(file config.ProjectFile, args []string) (ExecutionSpec, error) {
    spec := ExecutionSpec{Args: append([]string(nil), args...), Setup: file.Run.Setup, Env: maps.Clone(file.Run.Env)}
    if len(spec.Args) == 0 && strings.TrimSpace(file.Run.Command) != "" {
        spec.Args = []string{"/bin/sh", "-c", file.Run.Command}
    }
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "launch", Args: spec.Args, Setup: spec.Setup, Env: spec.Env}
    if err := validateCommand(req); err != nil {
        return ExecutionSpec{}, err
    }
    outputs := file.Outputs.Paths
    if len(outputs) == 0 {
        outputs = []string{"outputs"}
    }
    var err error
    spec.Outputs, err = sources.OutputPaths(outputs)
    return spec, err
}

// EnvironmentKeys exposes the configured names without their private values.
func (s ExecutionSpec) EnvironmentKeys() []string {
    keys := []string{}
    for key := range s.Env {
        keys = append(keys, key)
    }
    slices.Sort(keys)
    return keys
}

func validateCommand(req wire.Request) error {
    if len(req.Args) == 0 || req.Args[0] == "" {
        return fault.New("BAD_CONFIG", "run_command", nil)
    }
    for _, arg := range append(append([]string(nil), req.Args...), req.Setup) {
        if strings.ContainsRune(arg, 0) {
            return fault.New("BAD_CONFIG", "run_command", nil)
        }
    }
    for key, value := range req.Env {
        if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
            return fault.New("BAD_CONFIG", "run_env", nil)
        }
        for i, r := range key {
            if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
                return fault.New("BAD_CONFIG", "run_env", nil)
            }
        }
    }
    data, err := json.Marshal(req)
    if err != nil || len(data) > 900*1024 {
        return fault.New("BAD_CONFIG", "run_command", nil)
    }
    return nil
}
