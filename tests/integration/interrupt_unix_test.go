//go:build !windows

package integration

import (
    "os"
    "os/exec"
    "testing"
)

func interruptibleCommand(cmd *exec.Cmd)                 {}
func interruptCommand(t *testing.T, cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }
