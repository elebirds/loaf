//go:build !windows

package sync

import (
    "io"
    "os"

    "github.com/elebirds/loaf/internal/filelock"
    "golang.org/x/sys/unix"
)

func lockDestination(path string) (io.Closer, error) {
    file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
    if err != nil {
        return nil, err
    }
    if err := filelock.Try(file); err != nil {
        file.Close()
        return nil, err
    }
    return file, nil
}
