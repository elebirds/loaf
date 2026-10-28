// Package queue defines admission and policy contracts independently of process
// supervision. A queue request and its eventual Run retain separate identities.
package queue

import (
    "errors"
    "time"
)

const Version = 1

// Request is the public queue identity and admission phase, never a private spec.
type Request struct {
    FormatVersion int        `json:"format_version"`
    ID            string     `json:"request_id"`
    TargetID      string     `json:"target_id"`
    ProjectID     string     `json:"project_id"`
    RunID         string     `json:"run_id"`
    Name          string     `json:"name,omitempty"`
    Tags          []string   `json:"tags,omitempty"`
    State         string     `json:"state"`
    Sequence      uint64     `json:"sequence"`
    CreatedAt     time.Time  `json:"created_at"`
    UpdatedAt     time.Time  `json:"updated_at"`
    AttemptedAt   *time.Time `json:"attempted_at,omitempty"`
    ReasonCode    string     `json:"reason_code,omitempty"`
}

// Status describes the shared machine/user admission ledger, not GPU isolation.
type Status struct {
    TargetID           string `json:"target_id"`
    Slots              int    `json:"slots"`
    Reserved           int    `json:"reserved"`
    Queued             int    `json:"queued"`
    Paused             bool   `json:"paused"`
    CoordinatorRunning bool   `json:"coordinator_running"`
}

// Candidate has already passed admission checks before policy selection.
type Candidate struct {
    ID       string
    Sequence uint64
}

// Policy orders or chooses eligible work; it cannot perform side effects.
type Policy interface {
    Select(candidates []Candidate, availableSlots int) ([]string, error)
}

var ErrSelection = errors.New("invalid queue policy selection")

// ValidateSelection prevents policy implementations from bypassing admission.
func ValidateSelection(candidates []Candidate, slots int, selected []string) error {
    if slots < 0 || len(selected) > slots {
        return ErrSelection
    }
    eligible := make(map[string]bool, len(candidates))
    for _, c := range candidates {
        if c.ID == "" || eligible[c.ID] {
            return ErrSelection
        }
        eligible[c.ID] = true
    }
    for _, id := range selected {
        if !eligible[id] {
            return ErrSelection
        }
        delete(eligible, id)
    }
    return nil
}

// Execution is private input for a queue-owned prepared Run, not a public view.
type Execution struct {
    Version string            `json:"version"`
    Root    string            `json:"root"`
    Args    []string          `json:"args"`
    Setup   string            `json:"setup,omitempty"`
    Env     map[string]string `json:"env,omitempty"`
    Outputs []string          `json:"outputs"`
}

// Command is the private machine-level control input carried over SSH.
type Command struct {
    Action      string     `json:"action"`
    ID          string     `json:"request_id,omitempty"`
    TargetID    string     `json:"target_id,omitempty"`
    HostBinding string     `json:"host_binding,omitempty"`
    Slots       *int       `json:"slots,omitempty"`
    Paused      *bool      `json:"paused,omitempty"`
    Request     *Request   `json:"request,omitempty"`
    Execution   *Execution `json:"execution,omitempty"`
    Limit       int        `json:"limit,omitempty"`
}

// Result preserves selected identities and admission facts on partial failures.
type Result struct {
    StateError string    `json:"state_error,omitempty"`
    Request    *Request  `json:"request,omitempty"`
    Requests   []Request `json:"requests,omitempty"`
    Status     *Status   `json:"status,omitempty"`
    Cleaned    []string  `json:"cleaned,omitempty"`
}
