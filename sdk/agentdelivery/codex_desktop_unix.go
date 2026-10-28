//go:build darwin || linux

package agentdelivery

import (
    "os"
    "path/filepath"
    "syscall"
)

func privateDesktopSocket(path string) error {
    socket, err := os.Lstat(path)
    if err != nil {
        return ErrUnavailable
    }
    directory, err := os.Lstat(filepath.Dir(path))
    if err != nil || !directory.IsDir() || socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm()&0077 != 0 || directory.Mode().Perm()&0077 != 0 {
        return ErrUnavailable
    }
    for _, info := range []os.FileInfo{socket, directory} {
        stat, ok := info.Sys().(*syscall.Stat_t)
        if !ok || stat.Uid != uint32(os.Geteuid()) {
            return ErrUnavailable
        }
    }
    return nil
}
