package main

import (
    "context"
    "errors"
    "io"
    "os"

    "golang.org/x/sys/unix"
)

// terminalPassword disables echo before waiting and restores it on every exit.
// Polling the canonical terminal keeps native editing while allowing Ctrl-C to
// cancel without a reader goroutine racing the terminal-state restoration.
func terminalPassword(ctx context.Context) (data []byte, err error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    fd := int(os.Stdin.Fd())
    original, err := unix.IoctlGetTermios(fd, readTermios)
    if err != nil {
        return nil, err
    }
    hidden := *original
    hidden.Lflag &^= unix.ECHO | unix.ECHONL
    if err := unix.IoctlSetTermios(fd, writeTermios, &hidden); err != nil {
        return nil, err
    }
    defer func() {
        if restore := unix.IoctlSetTermios(fd, writeTermios, original); restore != nil {
            clear(data)
            data = nil
            err = restore
        }
    }()
    var buffer [4097]byte
    defer clear(buffer[:])
    for {
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
        n, err := unix.Poll(fds, 100)
        if errors.Is(err, unix.EINTR) {
            continue
        }
        if err != nil {
            return nil, err
        }
        if n == 0 {
            continue
        }
        count, err := unix.Read(fd, buffer[:])
        if errors.Is(err, unix.EINTR) {
            continue
        }
        if err != nil {
            return nil, err
        }
        if count == 0 {
            return nil, io.EOF
        }
        for _, b := range buffer[:count] {
            if b == '\n' || b == '\r' {
                return data, nil
            }
            data = append(data, b)
            if len(data) > 4096 {
                clear(data)
                return nil, io.ErrShortBuffer
            }
        }
    }
}
