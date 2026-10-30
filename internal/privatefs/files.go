// Package privatefs implements the small OS boundary for private local files.
// It never repairs the permissions or ownership of an existing file.
package privatefs

import (
    "crypto/rand"
    "encoding/hex"
    "errors"
    "os"
    "path/filepath"
)

// ErrUnsafe identifies a link, unexpected object, owner or public permissions.
var ErrUnsafe = errors.New("unsafe private file")

// Mkdir exclusively creates one directory with private permissions.
func Mkdir(path string) error { return mkdir(path) }

// MkdirAll creates missing directories privately without changing existing ones.
// Call Check separately when an existing directory must also be private.
func MkdirAll(path string) error {
    info, err := os.Lstat(path)
    if err == nil {
        if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
            return ErrUnsafe
        }
        return nil
    }
    if !os.IsNotExist(err) {
        return err
    }
    parent := filepath.Dir(path)
    if parent == path {
        return err
    }
    if err := MkdirAll(parent); err != nil {
        return err
    }
    if err := mkdir(path); err != nil && !os.IsExist(err) {
        return err
    }
    return Check(path, true)
}

// CreateTemp creates an empty, private regular file with exclusive creation.
func CreateTemp(dir string) (*os.File, error) {
    for range 100 {
        var id [16]byte
        if _, err := rand.Read(id[:]); err != nil {
            return nil, err
        }
        file, err := Create(filepath.Join(dir, ".loaf-"+hex.EncodeToString(id[:])))
        if !os.IsExist(err) {
            return file, err
        }
    }
    return nil, os.ErrExist
}
