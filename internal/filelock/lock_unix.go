//go:build !windows

package filelock

import (
    "errors"
    "os"

    "golang.org/x/sys/unix"
)

// Try locks a file immediately; closing its handle releases the lock.
func Try(file *os.File) error {
    err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
    if errors.Is(err, unix.EWOULDBLOCK) {
        return ErrBusy
    }
    return err
}
