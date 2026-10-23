package config

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/fault"
)

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

// RunObservation is stored separately to preserve the run locator format.
type RunObservation struct {
    Last    *ObservedRun  `json:"last_observation,omitempty"`
    Failure *QueryFailure `json:"last_query_failure,omitempty"`
}

type observationRecord struct {
    FormatVersion int    `json:"format_version"`
    ProjectID     string `json:"project_id"`
    RunID         string `json:"run_id"`
    RunObservation
}

func (s *Store) observationFile(id string) string {
    return filepath.Join(filepath.Dir(s.IndexFile), "run-observations", id+".json")
}

// RunObservation reads dated public facts without creating state.
func (s *Store) RunObservation(projectID, id string) (RunObservation, error) {
    if !projectIDPattern.MatchString(projectID) || !runIDPattern.MatchString(id) {
        return RunObservation{}, fault.New("BAD_CONFIG", "run_state", nil)
    }
    data, err := os.ReadFile(s.observationFile(id))
    if errors.Is(err, os.ErrNotExist) {
        return RunObservation{}, nil
    }
    if err != nil {
        return RunObservation{}, fault.Wrap("BAD_CONFIG", "run_state", err)
    }
    var r observationRecord
    d := json.NewDecoder(bytes.NewReader(data))
    d.DisallowUnknownFields()
    if err := d.Decode(&r); err != nil {
        return RunObservation{}, fault.Wrap("BAD_CONFIG", "run_state", err)
    }
    if r.FormatVersion != 1 || d.Decode(new(any)) != io.EOF {
        return RunObservation{}, fault.New("BAD_CONFIG", "unknown_format", nil)
    }
    if r.ProjectID != projectID || r.RunID != id {
        return RunObservation{}, fault.New("BAD_CONFIG", "run_state", nil)
    }
    return r.RunObservation, nil
}

// SaveRunObservation merges newer observations atomically; concurrent stale reads
// cannot replace a more recent success or failure. Unknown formats are preserved.
func (s *Store) SaveRunObservation(ctx context.Context, projectID, id string, next RunObservation) error {
    if !projectIDPattern.MatchString(projectID) || !runIDPattern.MatchString(id) {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    return locked(ctx, s.observationFile(id), func() error {
        refs, err := s.Runs(projectID)
        if err != nil {
            return err
        }
        found := false
        for _, ref := range refs {
            if ref.ID == id {
                found = true
                break
            }
        }
        if !found {
            return fault.New("RUN_NOT_FOUND", "run_not_found", nil)
        }
        old, err := s.RunObservation(projectID, id)
        if err != nil {
            return err
        }
        if next.Last != nil && (old.Last == nil || next.Last.ObservedAt.After(old.Last.ObservedAt)) {
            old.Last = next.Last
        }
        if next.Failure != nil && (old.Failure == nil || next.Failure.ObservedAt.After(old.Failure.ObservedAt)) {
            old.Failure = next.Failure
        }
        data, err := json.MarshalIndent(observationRecord{1, projectID, id, old}, "", "    ")
        if err != nil {
            return err
        }
        return atomicfile.Write(s.observationFile(id), append(data, '\n'))
    })
}
