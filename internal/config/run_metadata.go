package config

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "slices"
    "strings"
    "unicode"
    "unicode/utf8"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/fault"
)

// RunMetadata is human navigation, not an execution identity or remote request.
// Its separate storage leaves older clients' run index contract unchanged.
type RunMetadata struct {
    Name string   `json:"name,omitempty"`
    Tags []string `json:"tags,omitempty"`
}

// NormalizeRunMetadata bounds printable labels and canonicalizes exact tags.
func NormalizeRunMetadata(m RunMetadata) (RunMetadata, error) {
    valid := func(s string, limit int) bool {
        return utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit && !strings.ContainsFunc(s, unicode.IsControl)
    }
    if !valid(m.Name, 128) || len(m.Tags) > 16 {
        return RunMetadata{}, fault.New("BAD_CONFIG", "run_metadata_invalid", nil)
    }
    m.Tags = append([]string(nil), m.Tags...)
    for _, tag := range m.Tags {
        if tag == "" || !valid(tag, 64) {
            return RunMetadata{}, fault.New("BAD_CONFIG", "run_metadata_invalid", nil)
        }
    }
    slices.Sort(m.Tags)
    m.Tags = slices.Compact(m.Tags)
    return m, nil
}

type runMetadataRecord struct {
    FormatVersion int         `json:"format_version"`
    ProjectID     string      `json:"project_id"`
    RunID         string      `json:"run_id"`
    Metadata      RunMetadata `json:"metadata"`
}

func (s *Store) metadataFile(id string) string {
    return filepath.Join(filepath.Dir(s.IndexFile), "run-metadata", id+".json")
}

// RunMetadata reads one bound run's annotations without creating local state.
func (s *Store) RunMetadata(projectID, id string) (RunMetadata, error) {
    if !projectIDPattern.MatchString(projectID) || !runIDPattern.MatchString(id) {
        return RunMetadata{}, fault.New("BAD_CONFIG", "run_state", nil)
    }
    data, err := os.ReadFile(s.metadataFile(id))
    if errors.Is(err, os.ErrNotExist) {
        return RunMetadata{}, nil
    }
    if err != nil {
        return RunMetadata{}, fault.Wrap("BAD_CONFIG", "run_state", err)
    }
    var r runMetadataRecord
    d := json.NewDecoder(bytes.NewReader(data))
    d.DisallowUnknownFields()
    if err := d.Decode(&r); err != nil {
        return RunMetadata{}, fault.Wrap("BAD_CONFIG", "run_state", err)
    }
    if d.Decode(new(any)) != io.EOF || r.FormatVersion != 1 {
        return RunMetadata{}, fault.New("BAD_CONFIG", "unknown_format", nil)
    }
    if r.ProjectID != projectID || r.RunID != id {
        return RunMetadata{}, fault.New("BAD_CONFIG", "run_state", nil)
    }
    return NormalizeRunMetadata(r.Metadata)
}

// SaveRunMetadata saves immutable annotations before submission. It never
// changes an existing locator, credential reference, or older index format.
func (s *Store) SaveRunMetadata(ctx context.Context, projectID, id string, m RunMetadata) error {
    m, err := NormalizeRunMetadata(m)
    if err != nil {
        return err
    }
    if !projectIDPattern.MatchString(projectID) || !runIDPattern.MatchString(id) {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    return locked(ctx, s.metadataFile(id), func() error {
        index, err := s.loadRuns()
        if err != nil {
            return err
        }
        ref, ok := index.Runs[id]
        if !ok || ref.ProjectID != projectID {
            return fault.New("RUN_NOT_FOUND", "run_not_found", nil)
        }
        if _, err := os.Stat(s.metadataFile(id)); err == nil {
            old, err := s.RunMetadata(projectID, id)
            if err != nil {
                return err
            }
            if old.Name != m.Name || !slices.Equal(old.Tags, m.Tags) {
                return fault.New("BAD_CONFIG", "run_metadata_immutable", nil)
            }
            return nil
        } else if !os.IsNotExist(err) {
            return err
        }
        data, err := json.MarshalIndent(runMetadataRecord{1, projectID, id, m}, "", "    ")
        if err != nil {
            return err
        }
        return atomicfile.Write(s.metadataFile(id), append(data, '\n'))
    })
}
