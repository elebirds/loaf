//go:build !windows

package ssh

import (
    "errors"
    "os"
    "os/exec"
    "syscall"
    "time"
)

func ownProcess(cmd *exec.Cmd) (processOwner, error) {
    return processOwner{close: func() {}, started: func() error { return nil }}, nil
}

func cancelTransfer(cmd *process, graceful bool) {
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    signal := syscall.SIGKILL
    if graceful {
        signal = syscall.SIGTERM
    }
    cmd.Cancel = func() error {
        err := syscall.Kill(-cmd.Process.Pid, signal)
        if errors.Is(err, syscall.ESRCH) {
            return os.ErrProcessDone
        }
        return err
    }
}

func cancelForeground(cmd *process) {
    cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
    cmd.WaitDelay = time.Second
}
