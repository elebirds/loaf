//go:build !windows

package localpath

import "path/filepath"

// Canonical resolves an existing path to its filesystem identity spelling.
func Canonical(path string) (string, error) {
    resolved, err := filepath.EvalSymlinks(path)
    if err != nil {
        return "", err
    }
    return filepath.Abs(resolved)
}

// Supported accepts local paths on Unix; remote paths never use this function.
func Supported(path string) error { return nil }
