package atomicfile

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/privatefs"
)

func TestWindowsFailedReplacementRetainsCommittedFile(t *testing.T) {
    name := filepath.Join(t.TempDir(), "private", "state")
    if err := Write(name, []byte("committed")); err != nil {
        t.Fatal(err)
    }
    open, err := privatefs.Open(name, false)
    if err != nil {
        t.Fatal(err)
    }
    defer open.Close()
    if err := Write(name, []byte("replacement")); err == nil {
        t.Fatal("replaced a delete-locked file")
    }
    data, err := os.ReadFile(name)
    if err != nil || string(data) != "committed" {
        t.Fatal("lost committed bytes", err)
    }
    entries, err := os.ReadDir(filepath.Dir(name))
    if err != nil || len(entries) != 1 {
        t.Fatal("failed replacement leaked temporary files", entries, err)
    }
    open.Close()
    if err := Write(name, []byte("replacement")); err != nil {
        t.Fatal(err)
    }
    if err := privatefs.Check(name, false); err != nil {
        t.Fatal(err)
    }
}
