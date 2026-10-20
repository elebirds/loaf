// Package atomicfile provides durable replacement of private state files.
package atomicfile

import (
    "os"
    "path/filepath"
)

// Write privately replaces a file and syncs its containing directory.
func Write(path string, data []byte) error {
    if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
        return err
    }
    f, err := os.CreateTemp(filepath.Dir(path), ".loaf-*")
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
    if err = os.Rename(f.Name(), path); err != nil {
        return err
    }
    dir, err := os.Open(filepath.Dir(path))
    if err != nil {
        return err
    }
    defer dir.Close()
    return dir.Sync()
}
