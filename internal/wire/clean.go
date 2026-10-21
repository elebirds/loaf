package wire

import "time"

// RunSummary retains only a completed execution's public result, never its
// command, environment, or private supervisor identities.
type RunSummary struct {
    Status          string     `json:"status"`
    Phase           string     `json:"phase,omitempty"`
    CreatedAt       time.Time  `json:"created_at"`
    CompletedAt     *time.Time `json:"completed_at"`
    ExitCode        *int       `json:"exit_code"`
    Signal          string     `json:"signal,omitempty"`
    Reason          string     `json:"reason,omitempty"`
    StopGraceMillis *int64     `json:"stop_grace_millis,omitempty"`
}

// Summary projects a record onto the small public terminal result.
func Summary(record *RunRecord) RunSummary {
    return RunSummary{record.Status, record.Phase, record.CreatedAt, record.CompletedAt, record.ExitCode, record.Signal, record.Reason, record.StopGraceMillis}
}

// Valid reports whether this is a confirmed retained terminal outcome.
func (s RunSummary) Valid() bool {
    return Terminal(s.Status) && !s.CreatedAt.IsZero() && s.CompletedAt != nil && !s.CompletedAt.Before(s.CreatedAt)
}

// CleanupReceipt is the small deletion recovery marker kept outside a run's
// directory. A nil CleanedAt means the same explicit ID must be retried.
type CleanupReceipt struct {
    FormatVersion int        `json:"format_version"`
    Version       string     `json:"version"`
    Root          string     `json:"root"`
    ProjectID     string     `json:"project_id"`
    RunID         string     `json:"run_id"`
    Summary       RunSummary `json:"summary"`
    CleanedAt     *time.Time `json:"cleaned_at,omitempty"`
    Device        uint64     `json:"device"`
    Inode         uint64     `json:"inode"`
}
