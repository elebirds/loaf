//go:build !windows

package interaction

import (
    "context"
    "errors"
    "io"
    "os"

    "golang.org/x/sys/unix"
)

func readLine(ctx context.Context, input io.Reader, maxBytes int) ([]byte, error) {
    return readLineBytes(ctx, input, maxBytes)
}
func readByte(ctx context.Context, input io.Reader, one []byte) (int, error) {
    for {
        if err := ctx.Err(); err != nil {
            return 0, err
        }
        if file, ok := input.(*os.File); ok {
            fd := int(file.Fd())
            if fd < 0 {
                return 0, os.ErrClosed
            }
            fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
            n, err := unix.Poll(fds, 100)
            if errors.Is(err, unix.EINTR) {
                continue
            }
            if err != nil {
                return 0, err
            }
            if n == 0 {
                continue
            }
            if fds[0].Revents&unix.POLLNVAL != 0 {
                return 0, os.ErrClosed
            }
            if err := ctx.Err(); err != nil {
                return 0, err
            }
            n, err = unix.Read(fd, one)
            if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
                continue
            }
            if n == 0 && err == nil {
                err = io.EOF
            }
            return n, err
        }
        n, err := input.Read(one)
        if canceled := ctx.Err(); canceled != nil {
            return 0, canceled
        }
        if n == 0 && err == nil {
            return 0, io.ErrNoProgress
        }
        return n, err
    }
}

// ReadTerminalSecret disables local echo while retaining canonical editing. It
// restores the original terminal state before returning, including cancellation.
// The caller owns the returned bytes and must clear them after use.
func ReadTerminalSecret(ctx context.Context, input *os.File, maxBytes int) (value []byte, err error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    if input == nil {
        return nil, ErrTerminalRequired
    }
    fd := int(input.Fd())
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
        var restoreErr error
        if err != nil {
            // A canceled canonical read may still have an unfinished secret in
            // the kernel input queue. Discard it before returning the terminal.
            restoreErr = unix.IoctlSetTermios(fd, flushTermios, original)
        } else {
            restoreErr = unix.IoctlSetTermios(fd, writeTermios, original)
        }
        if restoreErr != nil {
            clear(value)
            value = nil
            err = errors.Join(err, restoreErr)
        }
    }()
    return readLine(ctx, input, byteLimit(maxBytes))
}
