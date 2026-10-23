package run

import (
    "slices"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/wire"
)

// RunPreview explains a local plan, not a submission or frozen source snapshot.
// It deliberately omits endpoint/authentication data and environment values.
type RunPreview struct {
    DryRun            bool                `json:"dry_run"`
    Root              string              `json:"root"`
    Target            string              `json:"target"`
    Args              []string            `json:"args"`
    SetupPresent      bool                `json:"setup_present"`
    EnvironmentKeys   []string            `json:"environment_keys"`
    RemoteBase        string              `json:"remote_base"`
    RemoteDirectory   string              `json:"remote_directory,omitempty"`
    ExistingProjectID string              `json:"existing_project_id,omitempty"`
    WaitMillis        int64               `json:"wait_millis"`
    Tail              int                 `json:"tail"`
    Outputs           []string            `json:"outputs"`
    Pull              config.PullPolicy   `json:"pull"`
    Source            sources.Plan        `json:"source"`
    Metadata          config.RunMetadata  `json:"metadata"`
    Provenance        map[string][]string `json:"provenance"`
}

// PreviewCommand validates the same command/environment contract as submission,
// but returns only the literal entry point and environment variable names.
func PreviewCommand(file config.ProjectFile, args []string) ([]string, []string, error) {
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "launch", Args: append([]string(nil), args...), Setup: file.Run.Setup, Env: file.Run.Env}
    if len(req.Args) == 0 && strings.TrimSpace(file.Run.Command) != "" {
        req.Args = []string{"/bin/sh", "-c", file.Run.Command}
    }
    if err := validateCommand(req); err != nil {
        return nil, nil, err
    }
    keys := []string{}
    for key := range file.Run.Env {
        keys = append(keys, key)
    }
    slices.Sort(keys)
    return req.Args, keys, nil
}
