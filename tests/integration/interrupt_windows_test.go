package integration

import (
    "os"
    "os/exec"
    "strconv"
    "syscall"
    "testing"

    "golang.org/x/sys/windows"
)

// Each fixture CLI owns a hidden console/group, so its control event cannot
// reach the user's terminal or another test process.
func interruptibleCommand(cmd *exec.Cmd) {
    cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}

func interruptCommand(t *testing.T, cmd *exec.Cmd) error {
    t.Helper()
    helper := exec.Command(os.Args[0], "-test.run=^TestWindowsInterruptHelper$")
    helper.Env = append(os.Environ(), "LOAF_INTEGRATION_INTERRUPT_PID="+strconv.Itoa(cmd.Process.Pid))
    output, err := helper.CombinedOutput()
    if err != nil {
        t.Log(string(output))
    }
    return err
}

func TestWindowsInterruptHelper(t *testing.T) {
    value := os.Getenv("LOAF_INTEGRATION_INTERRUPT_PID")
    if value == "" {
        return
    }
    pid, err := strconv.ParseUint(value, 10, 32)
    if err != nil {
        os.Exit(2)
    }
    kernel := windows.NewLazySystemDLL("kernel32.dll")
    kernel.NewProc("FreeConsole").Call()
    ok, _, _ := kernel.NewProc("AttachConsole").Call(uintptr(pid))
    if ok == 0 {
        os.Exit(3)
    }
    if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid)); err != nil {
        os.Exit(4)
    }
    os.Exit(0)
}
