//go:build !windows

package privatefs

import (
    "os"
    "path/filepath"
    "syscall"

    "golang.org/x/sys/unix"
)

func mkdir(path string) error { return os.Mkdir(path, 0700) }

// Check verifies an existing private regular file or directory without mutation.
func Check(path string, directory bool) error {
    info, err := os.Lstat(path)
    if err != nil {
        return err
    }
    return checkInfo(info, directory)
}

func checkInfo(info os.FileInfo, directory bool) error {
    owner, ok := info.Sys().(*syscall.Stat_t)
    if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 ||
        directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
        return ErrUnsafe
    }
    return nil
}

// Open opens a private regular file, creating it if requested, without links.
func Open(path string, create bool) (*os.File, error) {
    flags := os.O_RDWR | unix.O_NOFOLLOW | unix.O_NONBLOCK
    if create {
        flags |= os.O_CREATE
    }
    file, err := os.OpenFile(path, flags, 0600)
    if err != nil {
        return nil, err
    }
    info, err := file.Stat()
    if err == nil {
        err = checkInfo(info, false)
    }
    if err != nil {
        file.Close()
        return nil, err
    }
    return file, nil
}

// Create atomically creates a private regular file and refuses existing objects.
func Create(path string) (*os.File, error) {
    return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_NOFOLLOW, 0600)
}

// SyncParent makes a newly created file's directory entry durable.
func SyncParent(path string) error {
    dir, err := os.Open(filepath.Dir(path))
    if err != nil {
        return err
    }
    defer dir.Close()
    return dir.Sync()
}

// SyncAncestors persists new directory entries up to and including stop.
func SyncAncestors(path, stop string) error {
    for {
        if err := SyncParent(path); err != nil {
            return err
        }
        parent := filepath.Dir(path)
        if parent == stop || parent == path {
            return nil
        }
        path = parent
    }
}

// Replace publishes a previously flushed same-directory file durably.
func Replace(source, destination string) error {
    if err := os.Rename(source, destination); err != nil {
        return err
    }
    return SyncParent(destination)
}

// Remove removes a checked private file and durably records the deletion.
func Remove(path string) error {
    if err := Check(path, false); err != nil {
        return err
    }
    if err := os.Remove(path); err != nil {
        return err
    }
    return SyncParent(path)
}
