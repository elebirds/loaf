// Package remote owns Linux execution records and per-run supervision.
package remote

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "regexp"
    "strings"
    "syscall"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,80}$`)

// Serve reads exactly one request and writes exactly one response.
func Serve(in io.Reader, out io.Writer) int {
    var req wire.Request
    decoder := json.NewDecoder(io.LimitReader(in, 1<<20))
    decoder.DisallowUnknownFields()
    err := decoder.Decode(&req)
    if err == nil {
        var trailing any
        if decoder.Decode(&trailing) != io.EOF {
            err = errors.New("BAD_REQUEST")
        }
    }
    if err != nil {
        err = errors.New("BAD_REQUEST")
    }
    response := wire.Response{FormatVersion: 1, Version: wire.Version}
    if err == nil && req.Action == "outputs" {
        ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
        defer cancel()
        selection, selectionErr := Outputs(ctx, req)
        response.Outputs = &selection
        err = selectionErr
    } else if err == nil && (req.Action == "observe" || req.Action == "follow") {
        ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
        defer cancel()
        if req.Action == "follow" {
            if streamErr := Follow(ctx, req, out); streamErr != nil {
                _ = json.NewEncoder(out).Encode(wire.Response{FormatVersion: 1, Version: wire.Version, Error: &wire.Error{Code: streamErr.Error()}})
                return 1
            }
            return 0
        }
        observed, observeErr := Observe(ctx, req)
        response.Record = observed.Record
        response.Log = &observed.Log
        err = observeErr
    } else if err == nil {
        response.Record, err = Handle(req)
    }
    response.OK = err == nil
    if err != nil {
        response.Error = &wire.Error{Code: err.Error()}
        var pathErr *outputError
        if errors.As(err, &pathErr) {
            response.Error.Path = pathErr.path
        }
    }
    if err := json.NewEncoder(out).Encode(response); err != nil {
        return 1
    }
    if !response.OK {
        return 1
    }
    return 0
}

// Handle is the internal control surface reused by the client execution use cases.
func Handle(req wire.Request) (*wire.RunRecord, error) {
    if req.FormatVersion != wire.FormatVersion {
        return nil, errors.New("BAD_FORMAT")
    }
    if req.Version != wire.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    if req.Action == "version" {
        return nil, nil
    }
    dir, err := runPath(req)
    if err != nil {
        return nil, err
    }
    switch req.Action {
    case "start":
        return start(req, dir)
    case "launch":
        return launch(req, dir)
    case "status":
        return status(dir)
    case "stop":
        return stop(req, dir)
    default:
        return nil, errors.New("BAD_ACTION")
    }
}

func runPath(req wire.Request) (string, error) {
    if !identifier.MatchString(req.ProjectID) || !identifier.MatchString(req.RunID) || !filepath.IsAbs(req.Root) {
        return "", errors.New("UNSAFE_PATH")
    }
    root, err := filepath.EvalSymlinks(req.Root)
    if err != nil || root != filepath.Clean(req.Root) || root == "/" {
        return "", errors.New("UNSAFE_PATH")
    }
    home, err := os.UserHomeDir()
    if err != nil || root == home {
        return "", errors.New("UNSAFE_PATH")
    }
    markerFile, err := os.OpenFile(filepath.Join(root, ".loaf-project"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if err != nil {
        return "", errors.New("PROJECT_MISMATCH")
    }
    defer markerFile.Close()
    markerInfo, err := markerFile.Stat()
    if err != nil || !markerInfo.Mode().IsRegular() {
        return "", errors.New("PROJECT_MISMATCH")
    }
    marker, err := io.ReadAll(io.LimitReader(markerFile, 1024))
    if err != nil || strings.TrimSpace(string(marker)) != req.ProjectID {
        return "", errors.New("PROJECT_MISMATCH")
    }
    runs := filepath.Join(root, "runs")
    if req.Action != "status" && req.Action != "stop" {
        if err := os.MkdirAll(runs, 0700); err != nil {
            return "", errors.New("WRITE_FAILED")
        }
    } else if _, err := os.Lstat(runs); errors.Is(err, os.ErrNotExist) {
        return "", errors.New("RUN_NOT_FOUND")
    }
    real, err := filepath.EvalSymlinks(runs)
    if err != nil || real != runs {
        return "", errors.New("UNSAFE_PATH")
    }
    dir := filepath.Join(runs, req.RunID)
    if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
        return "", errors.New("UNSAFE_PATH")
    }
    return dir, nil
}

func start(req wire.Request, dir string) (*wire.RunRecord, error) {
    if err := validateExecution(req); err != nil {
        return nil, err
    }
    if _, err := os.Lstat(dir); err == nil {
        return existingStart(dir)
    }
    // Legacy cache-start callers can race before either has created run.json.
    // Claim the ID before the project lock so that race is reported as the same
    // uncertain submission, not as an unrelated project's busy cache.
    claim, err := os.OpenFile(filepath.Join(filepath.Dir(dir), "."+req.RunID+".claim"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
    if err != nil {
        return nil, errors.New("WRITE_FAILED")
    }
    defer claim.Close()
    if err := unix.Flock(int(claim.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        return existingStart(dir)
    }
    defer unix.Flock(int(claim.Fd()), unix.LOCK_UN)
    projectLock, err := lockProject(req.Root)
    if err != nil {
        if _, statErr := os.Lstat(dir); statErr == nil {
            return existingStart(dir)
        }
        return nil, err
    }
    defer projectLock.Close()
    if _, err := os.Lstat(dir); err == nil {
        return existingStart(dir)
    }
    state, err := loadCacheState(req.Root)
    if err != nil {
        return nil, err
    }
    if state != nil && !state.Complete {
        return nil, errors.New("SYNC_INCOMPLETE")
    }
    r, err := copyWork(req, dir)
    if err != nil {
        return r, err
    }
    if err := projectLock.Close(); err != nil {
        return r, errors.New("WRITE_FAILED")
    }
    return launch(req, dir)
}

// copyWork runs with the project lock held by its caller. A copied directory is
// not executable until its ready record is atomically committed.
func copyWork(req wire.Request, dir string) (*wire.RunRecord, error) {
    if err := os.Mkdir(dir, 0700); err != nil {
        if errors.Is(err, os.ErrExist) {
            return nil, errors.New("RUN_EXISTS")
        }
        return nil, errors.New("WRITE_FAILED")
    }
    r := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "starting", Phase: "copying", CreatedAt: time.Now().UTC()}
    if err := save(dir, r); err != nil {
        return r, err
    }
    source := filepath.Join(req.Root, "cache")
    real, err := filepath.EvalSymlinks(source)
    if err != nil || real != source {
        return r, failCopy(dir, r, "UNSAFE_SOURCE")
    }
    begin := time.Now()
    if err := os.CopyFS(filepath.Join(dir, "work"), os.DirFS(source)); err != nil {
        return r, failCopy(dir, r, "COPY_FAILED")
    }
    r.CopyMillis = time.Since(begin).Milliseconds()
    r.Phase = "prepared"
    return r, save(dir, r)
}

func failCopy(dir string, r *wire.RunRecord, reason string) error {
    now := time.Now().UTC()
    r.Status = "failed"
    r.Reason = reason
    r.CompletedAt = &now
    if err := save(dir, r); err != nil {
        return err
    }
    if reason == "UNSAFE_SOURCE" {
        return errors.New("UNSAFE_PATH")
    }
    return errors.New(reason)
}

// launch claims a prepared copy once. The cache is no longer consulted here.
func launch(req wire.Request, dir string) (*wire.RunRecord, error) {
    if err := validateExecution(req); err != nil {
        return nil, err
    }
    claim, err := os.OpenFile(filepath.Join(dir, "launch.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
    if err != nil {
        return nil, errors.New("RUN_NOT_FOUND")
    }
    defer claim.Close()
    if err := unix.Flock(int(claim.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        return existingStart(dir)
    }
    defer unix.Flock(int(claim.Fd()), unix.LOCK_UN)
    r, err := read(dir)
    if err != nil {
        return nil, err
    }
    if r.Phase != "prepared" {
        return existingStart(dir)
    }
    r.Request = req
    r.Phase = "starting"
    if err := save(dir, r); err != nil {
        return r, err
    }
    exe, err := os.Executable()
    if err != nil {
        return r, errors.New("START_FAILED")
    }
    null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
    if err != nil {
        return r, errors.New("START_FAILED")
    }
    defer null.Close()
    cmd := exec.Command(exe, "__supervise", dir)
    cmd.Stdin = null
    cmd.Stdout = null
    cmd.Stderr = null
    cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
    if err := cmd.Start(); err != nil {
        r.Status = "failed"
        r.Reason = "SUPERVISOR_START_FAILED"
        _ = save(dir, r)
        return r, errors.New("START_FAILED")
    }
    _ = cmd.Process.Release()
    deadline := time.Now().Add(3 * time.Second)
    for time.Now().Before(deadline) {
        r, err = read(dir)
        if err == nil && (r.Status != "starting" || r.SupervisorPID != 0) {
            return r, nil
        }
        time.Sleep(10 * time.Millisecond)
    }
    return r, errors.New("SUBMISSION_UNKNOWN")
}

func existingStart(dir string) (*wire.RunRecord, error) {
    r, err := status(dir)
    if err != nil && err.Error() == "RUN_NOT_FOUND" {
        return nil, errors.New("SUBMISSION_UNKNOWN")
    }
    return r, err
}

func terminal(s string) bool { return wire.Terminal(s) }

func read(dir string) (*wire.RunRecord, error) {
    file, err := os.OpenFile(filepath.Join(dir, "run.json"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if err != nil {
        if !errors.Is(err, os.ErrNotExist) {
            return nil, errors.New("BAD_FORMAT")
        }
        return nil, errors.New("RUN_NOT_FOUND")
    }
    defer file.Close()
    info, err := file.Stat()
    if err != nil || !info.Mode().IsRegular() {
        return nil, errors.New("BAD_FORMAT")
    }
    var r wire.RunRecord
    decoder := json.NewDecoder(io.LimitReader(file, 2<<20))
    if decoder.Decode(&r) != nil || decoder.Decode(new(any)) != io.EOF || r.FormatVersion != 1 {
        return nil, errors.New("BAD_FORMAT")
    }
    return &r, nil
}

func save(dir string, r *wire.RunRecord) error {
    data, err := json.MarshalIndent(r, "", "    ")
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    if err := atomicfile.Write(filepath.Join(dir, "run.json"), append(data, '\n')); err != nil {
        return errors.New("WRITE_FAILED")
    }
    return nil
}

func status(dir string) (*wire.RunRecord, error) {
    r, err := read(dir)
    if err != nil {
        return nil, err
    }
    if terminal(r.Status) || r.Status == "unknown" {
        return r, nil
    }
    if r.SupervisorPID == 0 {
        r.Reason = "SUBMISSION_UNCONFIRMED"
        return r, nil
    }
    identity, err := process(r.SupervisorPID)
    boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
    f, lockErr := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    owned := false
    if lockErr == nil {
        defer f.Close()
        lockErr = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
        owned = errors.Is(lockErr, unix.EWOULDBLOCK)
        if lockErr == nil {
            _ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
        }
    }
    if err != nil || identity.uid != uint32(os.Geteuid()) || identity.start != r.SupervisorStart || strings.TrimSpace(string(boot)) != r.BootID || !owned {
        r.Status = "unknown"
        r.Reason = "SUPERVISOR_LOST"
    }
    return r, nil
}

type processInfo struct {
    state, start string
    group        int
    uid          uint32
}

func process(pid int) (processInfo, error) {
    var owner unix.Stat_t
    if err := unix.Stat(fmt.Sprintf("/proc/%d", pid), &owner); err != nil {
        return processInfo{}, err
    }
    data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
    if err != nil {
        return processInfo{}, err
    }
    end := strings.LastIndex(string(data), ") ")
    if end < 0 {
        return processInfo{}, errors.New("BAD_PROCESS")
    }
    fields := strings.Fields(string(data[end+2:]))
    if len(fields) < 20 {
        return processInfo{}, errors.New("BAD_PROCESS")
    }
    var group int
    if _, err := fmt.Sscan(fields[2], &group); err != nil {
        return processInfo{}, err
    }
    return processInfo{fields[0], fields[19], group, owner.Uid}, nil
}
