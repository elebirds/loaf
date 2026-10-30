//go:build !windows

// Package testutil contains isolated filesystem fixtures used by platform tests.
package testutil

import (
    "os"
    "testing"
)

// Public makes an existing fixture readable by other users and returns its restore function.
func Public(t testing.TB, path string) func() {
    t.Helper()
    info, err := os.Stat(path)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.Chmod(path, info.Mode().Perm()|0044); err != nil {
        t.Fatal(err)
    }
    return func() {
        if err := os.Chmod(path, info.Mode().Perm()); err != nil {
            t.Error(err)
        }
    }
}
