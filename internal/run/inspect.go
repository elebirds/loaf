package run

import (
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/wire"
)

// LocalDetails exposes saved navigation facts, never an inferred live status or
// raw connection arguments, credential references, or execution environments.
type LocalDetails struct {
    View
    Source        string     `json:"source"`
    ProjectID     string     `json:"project_id"`
    ProjectRoot   string     `json:"project_root"`
    AttemptedAt   *time.Time `json:"attempted_at,omitempty"`
    Outputs       []string   `json:"outputs"`
    ClientVersion string     `json:"client_version"`
    Compatible    bool       `json:"compatible"`
    Instance      *cloud.Ref `json:"instance,omitempty"`
    Destination   string     `json:"destination,omitempty"`
}

// Inspect works offline and across software versions without preparing a helper.
func (s Service) Inspect(project config.Project, id string) (LocalDetails, error) {
    ref, err := s.Select(project.ID, id)
    if err != nil {
        return LocalDetails{}, err
    }
    details := LocalDetails{View: view(ref, nil), Source: "local", ProjectID: ref.ProjectID, ProjectRoot: project.Root, AttemptedAt: ref.AttemptedAt, Outputs: append([]string(nil), ref.Outputs...), ClientVersion: wire.Version, Compatible: ref.Version == wire.Version, Destination: ref.Connection.Destination}
    if ref.Connection.Cloud != nil {
        instance := ref.Connection.Cloud.Ref
        details.Instance = &instance
    }
    return details, nil
}
