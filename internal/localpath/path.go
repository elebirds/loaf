// Package localpath keeps local filesystem path semantics separate from the
// slash-separated Linux paths sent to the runner.
package localpath

import (
    "net/url"
    "path/filepath"
    "strings"
)

// FileURL returns a SQLite-compatible absolute file URI, including drive letters.
func FileURL(path string) url.URL {
    value := filepath.ToSlash(path)
    if filepath.VolumeName(path) != "" && !strings.HasPrefix(value, "/") {
        value = "/" + value
    }
    return url.URL{Scheme: "file", Path: value}
}

// Contains reports lexical containment. Paths on distinct volumes cannot contain
// each other; they are valid independent download locations.
func Contains(parent, child string) bool {
    relative, err := filepath.Rel(parent, child)
    return err == nil && filepath.IsLocal(relative)
}
