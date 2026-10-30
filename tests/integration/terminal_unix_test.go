//go:build !windows

package integration

import (
    "io"
    "os/exec"
    "testing"

    "github.com/creack/pty"
)

func startTerminal(t *testing.T, cmd *exec.Cmd) (io.ReadWriteCloser, func() error) {
    t.Helper()
    terminal, err := pty.Start(cmd)
    if err != nil {
        t.Fatal(err)
    }
    return terminal, cmd.Wait
}
