// Package wire contains the small, language-independent client/runner contract.
package wire

import "time"

// FormatVersion is the current internal request and record format.
const FormatVersion = 1

// Version is set identically in both executables by the build script.
var Version = "dev"

// Request is one SSH stdin control document. Args are never interpreted as Shell code.
type Request struct {
    FormatVersion int      `json:"format_version"`
    Version       string   `json:"version"`
    Action        string   `json:"action"`
    Root          string   `json:"root,omitempty"`
    ProjectID     string   `json:"project_id,omitempty"`
    RunID         string   `json:"run_id,omitempty"`
    Args          []string `json:"args,omitempty"`
}

// RunRecord is persisted by the remote execution owner.
type RunRecord struct {
    FormatVersion   int        `json:"format_version"`
    Request         Request    `json:"request"`
    Status          string     `json:"status"`
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
}
