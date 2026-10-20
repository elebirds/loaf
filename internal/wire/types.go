// Package wire contains the small, language-independent client/runner contract.
package wire

import "time"

// FormatVersion is the current internal request and record format.
const FormatVersion = 1

// Stop defaults apply to new supervisors; existing runs keep their bound runner.
const (
    DefaultStopGrace = 10 * time.Second
    MaxStopGrace     = 5 * time.Minute
)

// Version is set identically in both executables by the build script.
var Version = "dev"

// Request is one SSH stdin control document. Args are never interpreted as Shell code.
type Request struct {
    FormatVersion int               `json:"format_version"`
    Version       string            `json:"version"`
    Action        string            `json:"action"`
    Root          string            `json:"root,omitempty"`
    ProjectID     string            `json:"project_id,omitempty"`
    RunID         string            `json:"run_id,omitempty"`
    Args          []string          `json:"args,omitempty"`
    Setup         string            `json:"setup,omitempty"`
    Env           map[string]string `json:"env,omitempty"`
    WaitMillis    int64             `json:"wait_millis,omitempty"`
    Tail          int               `json:"tail,omitempty"`
    GraceMillis   *int64            `json:"grace_millis,omitempty"`
}

// RunRecord is persisted by the remote execution owner.
type RunRecord struct {
    FormatVersion   int        `json:"format_version"`
    Request         Request    `json:"request"`
    Status          string     `json:"status"`
    Phase           string     `json:"phase,omitempty"`
    CreatedAt       time.Time  `json:"created_at"`
    CompletedAt     *time.Time `json:"completed_at,omitempty"`
    SupervisorPID   int        `json:"supervisor_pid,omitempty"`
    SupervisorStart string     `json:"supervisor_start,omitempty"`
    BootID          string     `json:"boot_id,omitempty"`
    ChildPID        int        `json:"child_pid,omitempty"`
    ExitCode        *int       `json:"exit_code"`
    Signal          string     `json:"signal,omitempty"`
    Reason          string     `json:"reason,omitempty"`
    CopyMillis      int64      `json:"copy_millis"`
    StopGraceMillis *int64     `json:"stop_grace_millis,omitempty"`
}

// Error is machine-readable; only the local CLI chooses explanatory language.
type Error struct {
    Code string `json:"code"`
}

// Response is the single control response on SSH stdout.
type Response struct {
    FormatVersion int        `json:"format_version"`
    Version       string     `json:"version"`
    OK            bool       `json:"ok"`
    Record        *RunRecord `json:"record,omitempty"`
    Error         *Error     `json:"error,omitempty"`
    Log           *LogChunk  `json:"log,omitempty"`
}

// LogChunk is a bounded UTF-8 view of the durable log; offset counts source bytes.
type LogChunk struct {
    Text      string `json:"text"`
    Offset    int64  `json:"offset"`
    Truncated bool   `json:"truncated"`
}

// Observation combines a status snapshot with a bounded log tail.
type Observation struct {
    Record *RunRecord `json:"record"`
    Log    LogChunk   `json:"log"`
}

// LogEvent belongs to a transient observation stream, not a stored event log.
type LogEvent struct {
    Type   string     `json:"type"`
    Record *RunRecord `json:"record,omitempty"`
    Log    LogChunk   `json:"log"`
}

// Terminal reports a known, retained execution outcome.
func Terminal(status string) bool {
    return status == "succeeded" || status == "failed" || status == "stopped"
}
