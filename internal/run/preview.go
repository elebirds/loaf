package run

import (
    "github.com/elebirds/loaf/internal/config"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
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
    Metadata          task.RunMetadata    `json:"metadata"`
    Notifications     []string            `json:"notifications,omitempty"`
    Provenance        map[string][]string `json:"provenance"`
}
