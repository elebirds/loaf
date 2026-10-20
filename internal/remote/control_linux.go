// Package remote owns Linux execution records and per-run supervision.
package remote

import (
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
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
    if err == nil {
        response.Record, err = Handle(req)
    }
    response.OK = err == nil
    if err != nil {
        response.Error = &wire.Error{Code: err.Error()}
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
    case "status":
        return status(dir)
    case "stop":
        r, err := status(dir)
        if err != nil {
            return nil, err
        }
        if terminal(r.Status) {
            return r, nil
        }
        if r.Status == "unknown" {
            return r, errors.New("STOP_UNCONFIRMED")
        }
        if err := atomicfile.Write(filepath.Join(dir, "stop.request"), []byte("stop\n")); err != nil {
            return r, errors.New("WRITE_FAILED")
        }
        return status(dir)
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
    marker, err := os.ReadFile(filepath.Join(root, ".loaf-project"))
    if err != nil || strings.TrimSpace(string(marker)) != req.ProjectID {
        return "", errors.New("PROJECT_MISMATCH")
    }
    runs := filepath.Join(root, "runs")
    if err := os.MkdirAll(runs, 0700); err != nil {
        return "", errors.New("WRITE_FAILED")
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
    if len(req.Args) == 0 || req.Args[0] == "" {
        return nil, errors.New("BAD_COMMAND")
    }
    if _, err := os.Lstat(dir); err == nil {
        return existingStart(dir)
    }
    projectLock, err := lockProject(req.Root)
    if err != nil {
        if _, statErr := os.Lstat(dir); statErr == nil {
            return existingStart(dir)
        }
        return nil, err
    }
    defer projectLock.Close()
    state, err := loadCacheState(req.Root)
    if err != nil {
        return nil, err
    }
    if state != nil && !state.Complete {
        return nil, errors.New("SYNC_INCOMPLETE")
    }
    if err := os.Mkdir(dir, 0700); err != nil {
        if errors.Is(err, os.ErrExist) {
            existing, err := status(dir)
            if err != nil && err.Error() == "RUN_NOT_FOUND" {
                return nil, errors.New("SUBMISSION_UNKNOWN")
            }
            return existing, err
        }
        return nil, errors.New("WRITE_FAILED")
    }
    r := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "starting", CreatedAt: time.Now().UTC()}
    if err := save(dir, r); err != nil {
        return nil, err
    }
    source := filepath.Join(req.Root, "cache")
    real, err := filepath.EvalSymlinks(source)
    if err != nil || real != source {
        r.Status = "failed"
        r.Reason = "UNSAFE_SOURCE"
        _ = save(dir, r)
        return r, errors.New("UNSAFE_PATH")
    }
    begin := time.Now()
    if err := os.CopyFS(filepath.Join(dir, "work"), os.DirFS(source)); err != nil {
        r.Status = "failed"
        r.Reason = "COPY_FAILED"
        _ = save(dir, r)
        return r, errors.New("COPY_FAILED")
    }
    r.CopyMillis = time.Since(begin).Milliseconds()
    if err := save(dir, r); err != nil {
        return r, err
    }
    // The source copy is now independent; training never holds the cache lock.
    if err := projectLock.Close(); err != nil {
        return r, errors.New("WRITE_FAILED")
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

func terminal(s string) bool { return s == "succeeded" || s == "failed" || s == "stopped" }

func read(dir string) (*wire.RunRecord, error) {
    data, err := os.ReadFile(filepath.Join(dir, "run.json"))
    if err != nil {
        return nil, errors.New("RUN_NOT_FOUND")
    }
    var r wire.RunRecord
    if json.Unmarshal(data, &r) != nil || r.FormatVersion != 1 {
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
    if terminal(r.Status) {
        return r, nil
    }
    if r.SupervisorPID == 0 {
        r.Reason = "SUBMISSION_UNCONFIRMED"
        return r, nil
    }
    identity, err := process(r.SupervisorPID)
    boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
    f, lockErr := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_RDWR, 0)
    owned := false
    if lockErr == nil {
        defer f.Close()
        lockErr = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
        owned = errors.Is(lockErr, unix.EWOULDBLOCK)
        if lockErr == nil {
            _ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
        }
    }
    if err != nil || identity.start != r.SupervisorStart || strings.TrimSpace(string(boot)) != r.BootID || !owned {
        r.Status = "unknown"
        r.Reason = "SUPERVISOR_LOST"
    }
    return r, nil
}

type processInfo struct {
    state, start string
    group        int
}

func process(pid int) (processInfo, error) {
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
    return processInfo{fields[0], fields[19], group}, nil
}
