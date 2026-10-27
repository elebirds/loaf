package task

import (
    "slices"
    "strings"
    "unicode"
    "unicode/utf8"

    "github.com/elebirds/loaf/internal/fault"
)

// RunMetadata is human navigation, not an execution identity or remote request.
// Labels are immutable and stored independently from the frozen locator.
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
