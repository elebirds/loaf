package testutil

import (
    "os"
    "testing"

    "github.com/elebirds/loaf/internal/privatefs"
)

// Private checks actual OS privacy, including ACLs on Windows.
func Private(t testing.TB, path string, directory bool) {
    t.Helper()
    if err := privatefs.Check(path, directory); err != nil {
        t.Fatal(path, err)
    }
}

// Symlink requires the real filesystem capability and records an explicit skip
// when Windows has neither Developer Mode nor the symbolic-link privilege.
func Symlink(t testing.TB, target, path string) {
    t.Helper()
    if err := os.Symlink(target, path); err != nil {
        t.Skipf("symbolic links unavailable: %v", err)
    }
}
