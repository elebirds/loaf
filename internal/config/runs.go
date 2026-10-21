package config

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "regexp"
    "sort"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// RunRef is a durable locator. The remote record remains the execution truth.
// Preparing references cannot become the implicit latest run; uncertainty is
// persisted before sending a request that could start execution.
type RunRef struct {
    ID          string      `json:"run_id"`
    ProjectID   string      `json:"project_id"`
    Target      string      `json:"target"`
    Connection  Target      `json:"connection"`
    RemoteDir   string      `json:"remote_dir"`
    Runner      string      `json:"runner"`
    Version     string      `json:"version"`
    Outputs     []string    `json:"outputs"`
    CreatedAt   time.Time   `json:"created_at"`
    AttemptedAt *time.Time  `json:"attempted_at,omitempty"`
    Submission  string      `json:"submission"`
    Cleanup     *RunCleanup `json:"cleanup,omitempty"`
}

// RunCleanup preserves availability separately from the original execution
// result. Pending attempts remain retryable using the same bound run ID.
type RunCleanup struct {
    RequestedAt time.Time       `json:"requested_at"`
    CleanedAt   *time.Time      `json:"cleaned_at,omitempty"`
    Summary     wire.RunSummary `json:"summary"`
}

type runIndex struct {
    FormatVersion int               `json:"format_version"`
    Runs          map[string]RunRef `json:"runs"`
}

var runIDPattern = regexp.MustCompile(`^r_[0-9a-f]{24}$`)

func (s *Store) runsFile() string { return filepath.Join(filepath.Dir(s.IndexFile), "runs.json") }

func (s *Store) loadRuns() (runIndex, error) {
    index := runIndex{2, map[string]RunRef{}}
    data, err := os.ReadFile(s.runsFile())
    if errors.Is(err, os.ErrNotExist) {
        return index, nil
    }
    if err != nil {
        return index, err
    }
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    if err := decoder.Decode(&index); err != nil {
        return index, err
    }
    if decoder.Decode(new(any)) != io.EOF {
        return index, errors.New("BAD_FORMAT")
    }
    if index.FormatVersion != 2 {
        return index, fault.New("BAD_CONFIG", "unknown_format", nil)
    }
    if index.Runs == nil {
        index.Runs = map[string]RunRef{}
    }
    for id, ref := range index.Runs {
        if id != ref.ID || !validRun(ref) {
            return index, errors.New("BAD_RUN_REFERENCE")
        }
    }
    return index, nil
}

func validRun(ref RunRef) bool {
    return runIDPattern.MatchString(ref.ID) && projectIDPattern.MatchString(ref.ProjectID) &&
        ValidName(ref.Target) && ValidRemotePath(ref.RemoteDir) && filepath.IsAbs(ref.Runner) &&
        ref.Version != "" && !ref.CreatedAt.IsZero() &&
        (ref.Cleanup == nil || !ref.Cleanup.RequestedAt.IsZero() && ref.Cleanup.Summary.Valid()) &&
        (ref.Submission == "preparing" || ((ref.Submission == "submitted" || ref.Submission == "uncertain") && ref.AttemptedAt != nil))
}

// SaveRun atomically saves a locator before submission, without changing projects.
func (s *Store) SaveRun(ctx context.Context, ref RunRef) error {
    if !validRun(ref) {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    err := locked(ctx, s.runsFile(), func() error {
        index, err := s.loadRuns()
        if err != nil {
            return err
        }
        if old, ok := index.Runs[ref.ID]; ok && (old.ProjectID != ref.ProjectID || old.RemoteDir != ref.RemoteDir || old.Runner != ref.Runner || old.Target != ref.Target) {
            return errors.New("RUN_REFERENCE_CONFLICT")
        }
        if old, ok := index.Runs[ref.ID]; ok && old.Cleanup != nil {
            // A stale submission/observation save cannot erase availability
            // metadata, change its confirmed summary, or resurrect a clean run.
            ref = old
        }
        index.Runs[ref.ID] = ref
        data, err := json.MarshalIndent(index, "", "    ")
        if err != nil {
            return err
        }
        return atomicfile.Write(s.runsFile(), append(data, '\n'))
    })
    if err != nil {
        return fault.Wrap("BAD_CONFIG", "run_state", err)
    }
    return nil
}

// Runs returns only one project's locators, newest submission attempt first.
func (s *Store) Runs(projectID string) ([]RunRef, error) {
    index, err := s.loadRuns()
    if err != nil {
        return nil, fault.Wrap("BAD_CONFIG", "run_state", err)
    }
    refs := []RunRef{}
    for _, ref := range index.Runs {
        if ref.ProjectID == projectID && ref.Submission != "preparing" {
            refs = append(refs, ref)
        }
    }
    sort.Slice(refs, func(i, j int) bool { return refs[i].AttemptedAt.After(*refs[j].AttemptedAt) })
    return refs, nil
}

// RecordCleanup updates only availability metadata under the run-index lock.
// Clearing a rejected attempt compares its timestamp, so a concurrent later
// attempt or confirmed deletion cannot be rolled back by a stale response.
func (s *Store) RecordCleanup(ctx context.Context, ref RunRef, cleanup *RunCleanup) error {
    if !validRun(ref) || cleanup != nil && (cleanup.RequestedAt.IsZero() || !cleanup.Summary.Valid()) {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    err := locked(ctx, s.runsFile(), func() error {
        index, err := s.loadRuns()
        if err != nil {
            return err
        }
        current, ok := index.Runs[ref.ID]
        if !ok || current.Submission != "submitted" || current.ProjectID != ref.ProjectID || current.RemoteDir != ref.RemoteDir || current.Runner != ref.Runner || current.Target != ref.Target {
            return errors.New("RUN_REFERENCE_CONFLICT")
        }
        if current.Cleanup != nil && current.Cleanup.CleanedAt != nil {
            return nil
        }
        if cleanup == nil && (ref.Cleanup == nil || current.Cleanup == nil || !current.Cleanup.RequestedAt.Equal(ref.Cleanup.RequestedAt)) {
            return nil
        }
        if cleanup != nil && cleanup.CleanedAt == nil && current.Cleanup != nil {
            return nil
        }
        current.Cleanup = cleanup
        index.Runs[ref.ID] = current
        data, err := json.MarshalIndent(index, "", "    ")
        if err != nil {
            return err
        }
        return atomicfile.Write(s.runsFile(), append(data, '\n'))
    })
    if err != nil {
        return fault.Wrap("BAD_CONFIG", "run_state", err)
    }
    return nil
}
