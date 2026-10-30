// Package filelock provides cancellable OS locks on private regular files.
// The lock file is never deleted: replacing it would split concurrent owners.
package filelock

import (
    "context"
    "errors"
    "os"
    "time"

    "github.com/elebirds/loaf/internal/privatefs"
)

var ErrBusy = errors.New("file is locked")

// Acquire obtains an exclusive lock. Zero wait is nonblocking; a negative wait
// waits until cancellation. Closing the returned file releases the OS lock.
func Acquire(ctx context.Context, path string, wait time.Duration) (*os.File, error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    file, err := privatefs.Open(path, true)
    if err != nil {
        return nil, err
    }
    var deadline <-chan time.Time
    if wait > 0 {
        timer := time.NewTimer(wait)
        defer timer.Stop()
        deadline = timer.C
    }
    for {
        err = Try(file)
        if err == nil {
            return file, nil
        }
        if !errors.Is(err, ErrBusy) || wait == 0 {
            file.Close()
            return nil, err
        }
        select {
        case <-ctx.Done():
            file.Close()
            return nil, ctx.Err()
        case <-deadline:
            file.Close()
            return nil, ErrBusy
        case <-time.After(10 * time.Millisecond):
        }
    }
}
