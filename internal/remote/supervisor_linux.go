package remote

import (
    "errors"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"
    "time"

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

// Supervise owns one command until its real exit result has been durably saved.
func Supervise(dir string) error {
    r, err := read(dir)
    if err != nil {
        return err
    }
    expected, err := runPath(r.Request)
    if err != nil || expected != dir {
        return errors.New("UNSAFE_PATH")
    }
    owner, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
    if err != nil {
        return err
    }
    defer owner.Close()
    if err := unix.Flock(int(owner.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        return err
    }
    defer unix.Flock(int(owner.Fd()), unix.LOCK_UN)
    r, err = read(dir)
    if err != nil {
        return err
    }
    if terminal(r.Status) || r.SupervisorPID != 0 {
        return errors.New("ALREADY_OWNED")
    }
    self, err := process(os.Getpid())
    if err != nil {
        return err
    }
    boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
    if err != nil {
        return err
    }
    r.SupervisorPID = os.Getpid()
    r.SupervisorStart = self.start
    r.BootID = strings.TrimSpace(string(boot))
    finish := func() error {
        lock, err := lockRun(dir)
        if err != nil {
            return err
        }
        defer lock.Close()
        if terminal(r.Status) {
            now := time.Now().UTC()
            r.CompletedAt = &now
        }
        return save(dir, r)
    }
    log, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
    if err != nil {
        return err
    }
    defer log.Close()
    phases := []struct {
        name string
        args []string
    }{}
    if r.Request.Setup != "" {
        phases = append(phases, struct {
            name string
            args []string
        }{"setup", []string{"/bin/sh", "-c", r.Request.Setup}})
    }
    phases = append(phases, struct {
        name string
        args []string
    }{"command", r.Request.Args})
    for _, phase := range phases {
        if err := executePhase(dir, r, phase.name, phase.args, log); err != nil {
            return err
        }
        if r.Status != "succeeded" {
            break
        }
    }
    if err := log.Sync(); err != nil {
        return err
    }
    return finish()
}

func executePhase(dir string, r *wire.RunRecord, phase string, args []string, log *os.File) error {
    // A stop accepted before this lock cannot slip through phase admission.
    lock, err := lockRun(dir)
    if err != nil {
        return err
    }
    request, err := readStop(dir)
    if err != nil {
        lock.Close()
        r.Status = "unknown"
        r.Reason = "STOP_REQUEST_INVALID"
        return nil
    }
    if request != nil {
        lock.Close()
        r.Status = "stopped"
        r.Reason = "STOP_BEFORE_START"
        r.ExitCode = nil
        r.Signal = ""
        r.ChildPID = 0
        r.StopGraceMillis = &request.GraceMillis
        return nil
    }
    command, err := startPhase(dir, r, phase, args, log)
    lock.Close()
    if err != nil || command == nil {
        return err
    }
    return waitPhase(dir, r, command)
}

func startPhase(dir string, r *wire.RunRecord, phase string, args []string, log *os.File) (*exec.Cmd, error) {
    r.Phase = phase
    r.ExitCode = nil
    r.Signal = ""
    r.ChildPID = 0
    r.Status = "running"
    if err := save(dir, r); err != nil {
        return nil, err
    }
    work := filepath.Join(dir, "work")
    program, err := executable(args[0], work, r.Request.Env)
    if err != nil {
        fmt.Fprintf(log, "%s: %v\n", args[0], err)
        r.Status = "failed"
        r.Reason = "EXEC_FAILED"
        return nil, nil
    }
    command := exec.Command(program, args[1:]...)
    command.Dir = work
    command.Env = commandEnvironment(r.Request.Env)
    command.Stdout = log
    command.Stderr = log
    command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    if err := command.Start(); err != nil {
        fmt.Fprintln(log, err)
        r.Status = "failed"
        r.Reason = "EXEC_FAILED"
        return nil, nil
    }
    r.ChildPID = command.Process.Pid
    if err := save(dir, r); err != nil {
        _ = unix.Kill(-r.ChildPID, unix.SIGKILL)
        _ = command.Wait()
        return nil, err
    }
    return command, nil
}

func waitPhase(dir string, r *wire.RunRecord, command *exec.Cmd) error {
    stopped := false
    var deadline time.Time
    killed := false
    for {
        var info unix.Siginfo
        err := unix.Waitid(unix.P_PID, r.ChildPID, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
        if err != nil && !errors.Is(err, unix.EINTR) {
            r.Status = "unknown"
            r.Reason = "WAIT_FAILED"
            return nil
        }
        if info.Signo != 0 {
            break
        }
        if !stopped {
            request, err := readStop(dir)
            if err != nil {
                // Leave ownership intact and keep waiting rather than guessing
                // a stop policy from a damaged request.
                time.Sleep(20 * time.Millisecond)
                continue
            }
            if request != nil {
                stopped = true
                r.StopGraceMillis = &request.GraceMillis
                deadline = time.Now().Add(time.Duration(request.GraceMillis) * time.Millisecond)
                _ = unix.Kill(-r.ChildPID, unix.SIGTERM)
            }
        }
        if stopped && !killed && !time.Now().Before(deadline) {
            _ = unix.Kill(-r.ChildPID, unix.SIGKILL)
            killed = true
        }
        time.Sleep(20 * time.Millisecond)
    }
    // Keep the unreaped group leader as an identity anchor until the last signal.
    unconfirmed := false
    if groupAlive(r.ChildPID) {
        if deadline.IsZero() {
            _ = unix.Kill(-r.ChildPID, unix.SIGTERM)
            deadline = time.Now().Add(wire.DefaultStopGrace)
        }
        for groupAlive(r.ChildPID) && time.Now().Before(deadline) {
            time.Sleep(10 * time.Millisecond)
        }
        if groupAlive(r.ChildPID) && !killed {
            _ = unix.Kill(-r.ChildPID, unix.SIGKILL)
        }
        deadline = time.Now().Add(5 * time.Second)
        for groupAlive(r.ChildPID) && time.Now().Before(deadline) {
            time.Sleep(10 * time.Millisecond)
        }
        if groupAlive(r.ChildPID) {
            unconfirmed = true
        }
    }
    _ = command.Wait()
    if command.ProcessState == nil {
        r.Status = "unknown"
        r.Reason = "WAIT_FAILED"
        return nil
    }
    wait := command.ProcessState.Sys().(syscall.WaitStatus)
    r.Status = "failed"
    if wait.Exited() {
        code := wait.ExitStatus()
        r.ExitCode = &code
        if code == 0 {
            r.Status = "succeeded"
        }
    }
    if wait.Signaled() {
        r.Signal = wait.Signal().String()
    }
    if stopped {
        r.Status = "stopped"
    }
    if unconfirmed {
        r.Status = "unknown"
        r.Reason = "GROUP_TERMINATION_UNCONFIRMED"
    }
    return nil
}

func groupAlive(group int) bool {
    entries, err := os.ReadDir("/proc")
    if err != nil {
        return true
    }
    for _, entry := range entries {
        pid, err := strconv.Atoi(entry.Name())
        if err != nil {
            continue
        }
        p, err := process(pid)
        if err == nil && p.group == group && p.state != "Z" {
            return true
        }
    }
    return false
}
