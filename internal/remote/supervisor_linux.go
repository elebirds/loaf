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
    finish := func() error { now := time.Now().UTC(); r.CompletedAt = &now; return save(dir, r) }
    if requestedStop(dir) {
        r.Status = "stopped"
        r.Reason = "STOP_BEFORE_START"
        return finish()
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
        if requestedStop(dir) {
            r.Status = "stopped"
            r.Reason = "STOP_BEFORE_START"
            return finish()
        }
        r.Phase = phase.name
        if err := executePhase(dir, r, phase.args, log); err != nil {
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

func executePhase(dir string, r *wire.RunRecord, args []string, log *os.File) error {
    r.ExitCode = nil
    r.Signal = ""
    r.ChildPID = 0
    r.Status = "running"
    if err := save(dir, r); err != nil {
        return err
    }
    work := filepath.Join(dir, "work")
    program, err := executable(args[0], work, r.Request.Env)
    if err != nil {
        fmt.Fprintf(log, "%s: %v\n", args[0], err)
        r.Status = "failed"
        r.Reason = "EXEC_FAILED"
        return nil
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
        return nil
    }
    r.ChildPID = command.Process.Pid
    if err := save(dir, r); err != nil {
        _ = unix.Kill(-r.ChildPID, unix.SIGKILL)
        _ = command.Wait()
        return err
    }
    stopped := false
    var stopTime time.Time
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
        if !stopped && requestedStop(dir) {
            stopped = true
            stopTime = time.Now()
            _ = unix.Kill(-r.ChildPID, unix.SIGTERM)
        }
        if stopped && time.Since(stopTime) > 300*time.Millisecond {
            _ = unix.Kill(-r.ChildPID, unix.SIGKILL)
        }
        time.Sleep(20 * time.Millisecond)
    }
    // Keep the unreaped group leader as an identity anchor until the last signal.
    if groupAlive(r.ChildPID) {
        _ = unix.Kill(-r.ChildPID, unix.SIGTERM)
        deadline := time.Now().Add(300 * time.Millisecond)
        for groupAlive(r.ChildPID) && time.Now().Before(deadline) {
            time.Sleep(10 * time.Millisecond)
        }
        if groupAlive(r.ChildPID) {
            _ = unix.Kill(-r.ChildPID, unix.SIGKILL)
        }
    }
    _ = command.Wait()
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
    return nil
}

func requestedStop(dir string) bool {
    _, err := os.Stat(filepath.Join(dir, "stop.request"))
    return err == nil
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
