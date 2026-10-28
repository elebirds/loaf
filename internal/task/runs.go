// Package task defines durable run identities and public observation values.
package task

import (
    "path/filepath"
    "regexp"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/wire"
)

// RunRef is a durable locator. The remote record remains the execution truth.
// Preparing references cannot become the implicit latest run; uncertainty is
// persisted before sending a request that could start execution.
type RunRef struct {
    ID             string        `json:"run_id"`
    ProjectID      string        `json:"project_id"`
    Target         string        `json:"target"`
    Connection     config.Target `json:"connection"`
    RemoteDir      string        `json:"remote_dir"`
    Runner         string        `json:"runner"`
    Version        string        `json:"version"`
    QueueRequestID string        `json:"queue_request_id,omitempty"`
    Outputs        []string      `json:"outputs"`
    CreatedAt      time.Time     `json:"created_at"`
    AttemptedAt    *time.Time    `json:"attempted_at,omitempty"`
    Submission     string        `json:"submission"`
    Cleanup        *RunCleanup   `json:"cleanup,omitempty"`
    Name           string        `json:"-"`
    Tags           []string      `json:"-"`
}

// RunCleanup preserves availability separately from the original execution
// result. Pending attempts remain retryable using the same bound run ID.
type RunCleanup struct {
    RequestedAt time.Time       `json:"requested_at"`
    CleanedAt   *time.Time      `json:"cleaned_at,omitempty"`
    Summary     wire.RunSummary `json:"summary"`
}

var projectIDPattern = regexp.MustCompile(`^p_[0-9a-f]{24}$`)
var runIDPattern = regexp.MustCompile(`^r_[0-9a-f]{24}$`)
var queueIDPattern = regexp.MustCompile(`^q_[0-9a-f]{24}$`)

// ValidRun checks a durable locator before it enters the state store.
func ValidRun(ref RunRef) bool {
    return runIDPattern.MatchString(ref.ID) && projectIDPattern.MatchString(ref.ProjectID) &&
        (ref.QueueRequestID == "" || queueIDPattern.MatchString(ref.QueueRequestID)) &&
        config.ValidName(ref.Target) && config.ValidRemotePath(ref.RemoteDir) && filepath.IsAbs(ref.Runner) &&
        ref.Version != "" && !ref.CreatedAt.IsZero() &&
        (ref.Cleanup == nil || !ref.Cleanup.RequestedAt.IsZero() && ref.Cleanup.Summary.Valid()) &&
        (ref.Submission == "preparing" || ((ref.Submission == "submitted" || ref.Submission == "uncertain") && ref.AttemptedAt != nil))
}
