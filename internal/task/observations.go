package task

import "time"

// ObservedRun is a dated public observation, never a live status or private record.
type ObservedRun struct {
    ObservedAt  time.Time  `json:"observed_at"`
    Status      string     `json:"status"`
    Phase       string     `json:"phase,omitempty"`
    ExitCode    *int       `json:"exit_code"`
    Signal      string     `json:"signal,omitempty"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// QueryFailure retains only a stable failure code and stage, without diagnostics.
type QueryFailure struct {
    ObservedAt time.Time `json:"observed_at"`
    Code       string    `json:"code"`
    Stage      string    `json:"stage"`
}

// RunObservation keeps a successful observation and a later query failure independent.
type RunObservation struct {
    Last    *ObservedRun  `json:"last_observation,omitempty"`
    Failure *QueryFailure `json:"last_query_failure,omitempty"`
}
