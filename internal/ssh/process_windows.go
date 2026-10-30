package ssh

import (
    "context"
    "errors"
    "os"
    "os/exec"
    "sync"
    "syscall"
    "unsafe"

    "golang.org/x/sys/windows"
)

var processLifetime struct {
    once sync.Once
    job  windows.Handle
    err  error
}

func killOnCloseJob() (windows.Handle, error) {
    job, err := windows.CreateJobObject(nil, nil)
    if err != nil {
        return 0, err
    }
    limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
    limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
    if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
        windows.CloseHandle(job)
        return 0, err
    }
    return job, nil
}

// Join one lifetime job before creating any transport process. Every child
// inherits it atomically at creation. The sole non-inheritable handle remains
// open until this process exits, including an abrupt exit; there is no interval
// where a newly created, suspended child can outlive its owner.
func ownLifetime() error {
    processLifetime.once.Do(func() {
        job, err := killOnCloseJob()
        if err == nil {
            err = windows.AssignProcessToJobObject(job, windows.CurrentProcess())
            if err != nil {
                windows.CloseHandle(job)
            }
        }
        processLifetime.job, processLifetime.err = job, err
    })
    return processLifetime.err
}

// A suspended child already belongs to the lifetime job. Assign its nested,
// per-command job before allowing its initial thread to execute. Cancel and
// admission share a mutex, so cancellation cannot miss a not-yet-admitted child.
// Ordinary parentage preserves native console initialization and handles.
func ownProcess(cmd *exec.Cmd) (processOwner, error) {
    if err := ownLifetime(); err != nil {
        return processOwner{}, err
    }
    job, err := killOnCloseJob()
    if err != nil {
        return processOwner{}, err
    }
    restore := consoleModes(cmd)
    var once sync.Once
    close := func() { once.Do(func() { windows.CloseHandle(job); restore() }) }
    var mu sync.Mutex
    canceled := false
    cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, HideWindow: true}
    cmd.Cancel = func() error {
        mu.Lock()
        defer mu.Unlock()
        canceled = true
        return windows.TerminateJobObject(job, 255)
    }
    started := func() error {
        mu.Lock()
        defer mu.Unlock()
        if canceled {
            return context.Canceled
        }
        process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
        if err != nil {
            return err
        }
        defer windows.CloseHandle(process)
        if err := windows.AssignProcessToJobObject(job, process); err != nil {
            return err
        }
        return resumeInitialThread(uint32(cmd.Process.Pid))
    }
    return processOwner{close: close, started: started}, nil
}

func resumeInitialThread(pid uint32) error {
    snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
    if err != nil {
        return err
    }
    defer windows.CloseHandle(snapshot)
    entry := windows.ThreadEntry32{}
    entry.Size = uint32(unsafe.Sizeof(entry))
    err = windows.Thread32First(snapshot, &entry)
    for err == nil {
        if entry.OwnerProcessID == pid {
            thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
            if err != nil {
                return err
            }
            defer windows.CloseHandle(thread)
            previous, err := windows.ResumeThread(thread)
            if err != nil {
                return err
            }
            if previous != 1 {
                return errors.New("unexpected initial thread suspension")
            }
            return nil
        }
        err = windows.Thread32Next(snapshot, &entry)
    }
    return errors.New("suspended initial thread not found")
}

// Forced process-tree cancellation cannot run OpenSSH's terminal cleanup.
// Restore only the actual console handles supplied to this command.
func consoleModes(cmd *exec.Cmd) func() {
    modes := map[windows.Handle]uint32{}
    for _, stream := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
        if file, ok := stream.(*os.File); ok {
            handle := windows.Handle(file.Fd())
            var mode uint32
            if windows.GetConsoleMode(handle, &mode) == nil {
                modes[handle] = mode
            }
        }
    }
    return func() {
        for handle, mode := range modes {
            windows.SetConsoleMode(handle, mode)
        }
    }
}

func cancelTransfer(cmd *process, graceful bool) {}
func cancelForeground(cmd *process)              {}
