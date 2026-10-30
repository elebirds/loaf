// Package atomicfile provides durable replacement of private state files.
package atomicfile

import (
    "os"
    "path/filepath"

    "github.com/elebirds/loaf/internal/privatefs"
)

// Write privately replaces a file and syncs its containing directory.
func Write(path string, data []byte) error {
    if err := privatefs.MkdirAll(filepath.Dir(path)); err != nil {
        return err
    }
    f, err := privatefs.CreateTemp(filepath.Dir(path))
    if err != nil {
        return err
    }
    defer os.Remove(f.Name())
    if _, err = f.Write(data); err == nil {
        err = f.Sync()
    }
    if closeErr := f.Close(); err == nil {
        err = closeErr
    }
    if err != nil {
        return err
    }
    return privatefs.Replace(f.Name(), path)
}
