//go:build darwin || linux

package agentdelivery

import (
    "os"
    "path/filepath"
    "syscall"
)

func privateSocketDirectory(path string) error {
    info, err := os.Lstat(path)
    if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
        return ErrUnavailable
    }
    owner, ok := info.Sys().(*syscall.Stat_t)
    if !ok || owner.Uid != uint32(os.Geteuid()) {
        return ErrUnavailable
    }
    return nil
}

// Only the public native Codex transport permits its short-path symlink.
func privateSocket(path string, allowSymlink bool) error {
    info, err := os.Lstat(path)
    if err != nil {
        return ErrUnavailable
    }
    if info.Mode()&os.ModeSymlink != 0 {
        owner, ok := info.Sys().(*syscall.Stat_t)
        if !allowSymlink || !ok || owner.Uid != uint32(os.Geteuid()) {
            return ErrUnavailable
        }
        if privateSocketDirectory(filepath.Dir(path)) != nil {
            return ErrUnavailable
        }
        path, err = filepath.EvalSymlinks(path)
        if err != nil {
            return ErrUnavailable
        }
        info, err = os.Lstat(path)
        if err != nil {
            return ErrUnavailable
        }
    }
    if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 || privateSocketDirectory(filepath.Dir(path)) != nil {
        return ErrUnavailable
    }
    owner, ok := info.Sys().(*syscall.Stat_t)
    if !ok || owner.Uid != uint32(os.Geteuid()) {
        return ErrUnavailable
    }
    return nil
}
