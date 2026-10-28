package remote

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "maps"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "slices"
    "strings"
    "syscall"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
    "github.com/elebirds/loaf/internal/queue/coordinator"
    queuestate "github.com/elebirds/loaf/internal/state/queue"
    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

// QueueDirectory is shared across projects and aliases for one remote user.
func QueueDirectory() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil || !filepath.IsAbs(home) {
        return "", errors.New("QUEUE_IDENTITY_MISMATCH")
    }
    return filepath.Join(home, ".local", "share", "loaf", "queue-v1"), nil
}

func queueIdentity(binding string) (queuestate.Identity, error) {
    host, err := os.Hostname()
    if err != nil || host == "" {
        return queuestate.Identity{}, errors.New("QUEUE_IDENTITY_MISMATCH")
    }
    machine, _ := os.ReadFile("/etc/machine-id")
    hardware, _ := os.ReadFile("/sys/class/dmi/id/product_uuid")
    identity := fmt.Sprintf("uid=%d;host=%s;machine=%s;hardware=%s", os.Geteuid(), host, strings.TrimSpace(string(machine)), strings.TrimSpace(string(hardware)))
    return queuestate.Identity{Host: identity, Binding: binding, Version: wire.Version}, nil
}

// QueueControl handles one explicit admission operation. Mutations are not replayable.
func QueueControl(ctx context.Context, req wire.Request) (queue.Result, error) {
    root, err := QueueDirectory()
    if err != nil {
        return queue.Result{}, err
    }
    binding := ""
    if req.Queue != nil {
        binding = req.Queue.HostBinding
    }
    identity, err := queueIdentity(binding)
    if err != nil {
        return queue.Result{}, err
    }
    return queueControlAt(ctx, req, root, identity, startCoordinator)
}

func queueControlAt(ctx context.Context, req wire.Request, root string, identity queuestate.Identity, start func(string) error) (result queue.Result, err error) {
    defer func() { err = queuestate.ErrorCode(err) }()
    if req.FormatVersion != wire.FormatVersion {
        return result, errors.New("BAD_FORMAT")
    }
    if req.Version != wire.Version {
        return result, errors.New("VERSION_MISMATCH")
    }
    if req.Queue == nil {
        return result, errors.New("QUEUE_BAD_REQUEST")
    }
    command := req.Queue
    write := command.Action != "list" && command.Action != "show" && command.Action != "status" && command.Action != "clean-preview"
    path := filepath.Join(root, "state.sqlite3")
    if command.Action != "enable" {
        if _, err = os.Lstat(path); err != nil {
            return result, err
        }
    }
    store, err := queuestate.Open(ctx, path, identity, write)
    if err != nil {
        return result, err
    }
    defer store.Close()
    if command.TargetID != "" && command.TargetID != store.TargetID {
        return result, errors.New("QUEUE_IDENTITY_MISMATCH")
    }
    request := func(e queuestate.Entry) {
        if e.Request.ID != "" {
            q := e.Request
            result.Request = &q
        }
    }
    switch command.Action {
    case "enable":
        slots := 1
        if command.Slots != nil {
            slots = *command.Slots
        }
        status, configureErr := store.Configure(ctx, slots)
        result.Status = &status
        if configureErr != nil {
            return result, configureErr
        }
        if err = start(root); err != nil {
            return result, err
        }
    case "prepare":
        if command.Request == nil || command.Execution == nil {
            return result, errors.New("QUEUE_BAD_REQUEST")
        }
        e := *command.Execution
        if err = validateQueueExecution(*command.Request, e); err != nil {
            return result, err
        }
        q := *command.Request
        q.TargetID = store.TargetID
        prepared, prepareErr := store.Prepare(ctx, q, e)
        request(prepared)
        if prepareErr != nil {
            return result, prepareErr
        }
        if prepared.Request.State != "preparing" {
            return result, errors.New("QUEUE_REQUEST_EXISTS")
        }
        if err = claimQueuedRun(root, prepared.Request.RunID); err != nil {
            return result, err
        }
    case "enqueue":
        e, entryErr := store.Entry(ctx, command.ID)
        if entryErr != nil {
            return result, entryErr
        }
        request(e)
        runReq := queuedExecution(e, "status")
        dir, pathErr := runPath(runReq)
        if pathErr != nil {
            return result, pathErr
        }
        lock, lockErr := lockProject(e.Execution.Root)
        if lockErr != nil {
            return result, lockErr
        }
        defer lock.Close()
        record, readErr := read(dir)
        if readErr != nil {
            return result, readErr
        }
        if record.Phase != "prepared" || record.SupervisorPID != 0 {
            return result, errors.New("QUEUE_INPUT_NOT_PREPARED")
        }
        admitted, admitErr := store.Admit(ctx, command.ID)
        request(admitted)
        if admitErr != nil {
            return result, admitErr
        }
        // Wake only an already-enabled queue; input preparation never launches work.
        if err = start(root); err != nil {
            return result, err
        }
    case "list":
        entries, more, listErr := store.List(ctx, command.Limit)
        if listErr != nil {
            return result, listErr
        }
        result.Requests = []queue.Request{}
        result.HasMore = more
        for _, e := range entries {
            result.Requests = append(result.Requests, e.Request)
        }
    case "show":
        e, entryErr := store.Entry(ctx, command.ID)
        if entryErr != nil {
            return result, entryErr
        }
        request(e)
    case "cancel":
        var e queuestate.Entry
        var cancelErr error
        if command.Request != nil {
            r := *command.Request
            if r.ID != command.ID || r.RunID != req.RunID || r.ProjectID != req.ProjectID || r.TargetID != store.TargetID || r.FormatVersion != queue.Version || r.CreatedAt.IsZero() || !identifier.MatchString(r.ID) || !identifier.MatchString(r.RunID) || !identifier.MatchString(r.ProjectID) || !filepath.IsAbs(req.Root) {
                return result, errors.New("QUEUE_BAD_REQUEST")
            }
            if err = claimQueuedRun(root, r.RunID); err != nil {
                return result, err
            }
            e, cancelErr = store.CancelUnprepared(ctx, r, queue.Execution{Version: wire.Version, Root: req.Root})
        } else {
            e, cancelErr = store.Cancel(ctx, command.ID)
        }
        request(e)
        if cancelErr != nil {
            return result, cancelErr
        }
    case "pause":
        if command.Paused == nil {
            return result, errors.New("QUEUE_BAD_REQUEST")
        }
        status, pauseErr := store.SetPaused(ctx, *command.Paused)
        result.Status = &status
        if pauseErr != nil {
            return result, pauseErr
        }
        if !*command.Paused {
            if err = start(root); err != nil {
                return result, err
            }
        }
    case "clean-preview", "clean":
        e, entryErr := store.Entry(ctx, command.ID)
        if entryErr != nil {
            return result, entryErr
        }
        request(e)
        if err = cleanQueuedInput(ctx, e, command.Action == "clean-preview"); err != nil {
            return result, err
        }
        if command.Action == "clean" {
            result.Cleaned = []string{e.Request.ID}
        }
    case "status":
    default:
        return result, errors.New("QUEUE_BAD_REQUEST")
    }
    status, statusErr := store.Status(ctx)
    status.CoordinatorRunning = coordinatorRunning(root)
    result.Status = &status
    return result, statusErr
}

func validateQueueExecution(q queue.Request, e queue.Execution) error {
    if e.Version != wire.Version {
        return errors.New("VERSION_MISMATCH")
    }
    if !identifier.MatchString(q.ProjectID) || !identifier.MatchString(q.RunID) || !identifier.MatchString(q.ID) || !filepath.IsAbs(e.Root) || filepath.Clean(e.Root) != e.Root || e.Root == "/" {
        return errors.New("QUEUE_BAD_REQUEST")
    }
    home, _ := os.UserHomeDir()
    if e.Root == home {
        return errors.New("UNSAFE_PATH")
    }
    return validateExecution(wire.Request{Args: e.Args, Setup: e.Setup, Env: e.Env})
}

func queuedExecution(e queuestate.Entry, action string) wire.Request {
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: e.Execution.Version, Action: action, Root: e.Execution.Root, ProjectID: e.Request.ProjectID, RunID: e.Request.RunID, Args: slices.Clone(e.Execution.Args), Setup: e.Execution.Setup, Env: maps.Clone(e.Execution.Env), Outputs: slices.Clone(e.Execution.Outputs)}
    if action == "launch" {
        req.QueueRequestID = e.Request.ID
        req.AttemptedAt = e.Request.AttemptedAt
    }
    return req
}

// QueueEvents reads the machine queue source independently of any Run.
func QueueEvents(ctx context.Context, req wire.Request) (event.Page, error) {
    if req.FormatVersion != wire.FormatVersion {
        return event.Page{}, errors.New("BAD_FORMAT")
    }
    if req.Version != wire.Version {
        return event.Page{}, errors.New("VERSION_MISMATCH")
    }
    if req.Events == nil {
        return event.Page{}, errors.New("EVENT_INVALID")
    }
    root, err := QueueDirectory()
    if err != nil {
        return event.Page{}, err
    }
    binding := ""
    if req.Queue != nil {
        binding = req.Queue.HostBinding
    }
    identity, err := queueIdentity(binding)
    if err != nil {
        return event.Page{}, err
    }
    store, err := queuestate.Open(ctx, filepath.Join(root, "state.sqlite3"), identity, false)
    if err != nil {
        return event.Page{}, queuestate.ErrorCode(err)
    }
    defer store.Close()
    page, err := store.Events(ctx, *req.Events)
    return page, queuestate.ErrorCode(err)
}

// QueueRunGuard affects only Run IDs already claimed by this queue. An unrelated
// direct Run never acquires queue prerequisites, even on a cloned queue database.
// Call under the existing project-copy or launch lock before the side effect.
func QueueRunGuard(req wire.Request, launching bool) error {
    root, err := QueueDirectory()
    if err != nil {
        return err
    }
    // The durable routing claim lets unrelated direct Runs bypass the queue
    // even when a queue database has a foreign version or cloned identity.
    // A queue prepare is not acknowledged until this claim is durable.
    if _, err = os.Lstat(filepath.Join(root, "claims", req.RunID)); errors.Is(err, os.ErrNotExist) {
        return nil
    } else if err != nil {
        return errors.New("QUEUE_UNSAFE_STATE")
    }
    store, err := queuestate.Open(context.Background(), filepath.Join(root, "state.sqlite3"), queuestate.Identity{}, false)
    if err != nil {
        return queuestate.ErrorCode(err)
    }
    e, err := store.Run(context.Background(), req.RunID)
    store.Close()
    if errors.Is(err, sql.ErrNoRows) {
        return errors.New("QUEUE_NO_START")
    }
    if err != nil {
        return queuestate.ErrorCode(err)
    }
    identity, err := queueIdentity("")
    if err != nil {
        return err
    }
    store, err = queuestate.Open(context.Background(), filepath.Join(root, "state.sqlite3"), identity, false)
    if err != nil {
        return queuestate.ErrorCode(err)
    }
    defer store.Close()
    if e.Request.ProjectID != req.ProjectID || e.Execution.Root != req.Root {
        return errors.New("QUEUE_IDENTITY_MISMATCH")
    }
    if launching {
        if e.Request.State != "dispatching" || !e.Attempted {
            return errors.New("QUEUE_NO_START")
        }
        if req.QueueRequestID != e.Request.ID || req.AttemptedAt == nil || e.Request.AttemptedAt == nil || !req.AttemptedAt.Equal(*e.Request.AttemptedAt) {
            return errors.New("QUEUE_IDENTITY_MISMATCH")
        }
        expected := queuedExecution(e, "launch")
        if !sameExecution(expected, req) {
            return errors.New("QUEUE_IDENTITY_MISMATCH")
        }
    } else if e.Request.State != "preparing" {
        return errors.New("QUEUE_NO_START")
    }
    return nil
}

func claimQueuedRun(root, id string) error {
    dir := filepath.Join(root, "claims")
    if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
        return errors.New("QUEUE_WRITE_FAILED")
    }
    var stat unix.Stat_t
    if unix.Lstat(dir, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
        return errors.New("QUEUE_UNSAFE_STATE")
    }
    file, err := os.OpenFile(filepath.Join(dir, id), os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_NOFOLLOW, 0600)
    if errors.Is(err, os.ErrExist) {
        if unix.Lstat(filepath.Join(dir, id), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
            return errors.New("QUEUE_UNSAFE_STATE")
        }
        return nil
    }
    if err != nil {
        return errors.New("QUEUE_WRITE_FAILED")
    }
    err = file.Sync()
    file.Close()
    if err != nil {
        return errors.New("QUEUE_WRITE_FAILED")
    }
    for _, name := range []string{dir, root} {
        parent, err := os.Open(name)
        if err != nil {
            return errors.New("QUEUE_WRITE_FAILED")
        }
        err = parent.Sync()
        parent.Close()
        if err != nil {
            return errors.New("QUEUE_WRITE_FAILED")
        }
    }
    return nil
}

// Coordinate is the detached, single-owner dispatch entrypoint. The executable
// invokes it as __coordinate with the fixed private QueueDirectory path.
func Coordinate(root string) error {
    expected, err := QueueDirectory()
    if err != nil || root != expected {
        return errors.New("QUEUE_UNSAFE_STATE")
    }
    identity, err := queueIdentity("")
    if err != nil {
        return err
    }
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
    defer cancel()
    store, err := queuestate.Open(ctx, filepath.Join(root, "state.sqlite3"), identity, true)
    if err != nil {
        return queuestate.ErrorCode(err)
    }
    defer store.Close()
    lock, err := queueOwnerLock(root, true)
    if err != nil {
        return err
    }
    defer lock.Close()
    engine := coordinator.Coordinator{Store: store, Policy: queue.FIFO{}, Runs: queueRuns{}}
    timer := time.NewTicker(250 * time.Millisecond)
    defer timer.Stop()
    for {
        if err = engine.Tick(ctx); err != nil && ctx.Err() == nil {
            queueDiagnostic(root, queuestate.ErrorCode(err).Error())
        }
        select {
        case <-ctx.Done():
            return nil
        case <-timer.C:
        }
    }
}

type queueRuns struct{}

func (queueRuns) Launch(ctx context.Context, e queuestate.Entry) (coordinator.Outcome, error) {
    if err := ctx.Err(); err != nil {
        return coordinator.Outcome{}, err
    }
    req := queuedExecution(e, "launch")
    if err := QueueRunGuard(req, true); err != nil {
        return coordinator.Outcome{Code: "QUEUE_NO_START"}, err
    }
    r, err := Handle(req)
    return queueOutcome(e, r), err
}
func (queueRuns) Observe(ctx context.Context, e queuestate.Entry) (coordinator.Outcome, error) {
    if err := ctx.Err(); err != nil {
        return coordinator.Outcome{}, err
    }
    r, err := Handle(queuedExecution(e, "status"))
    return queueOutcome(e, r), err
}
func queueOutcome(e queuestate.Entry, r *wire.RunRecord) coordinator.Outcome {
    if r == nil {
        return coordinator.Outcome{}
    }
    if r.Request.Root != e.Execution.Root || r.Request.RunID != e.Request.RunID || r.Request.ProjectID != e.Request.ProjectID || r.Request.Version != e.Execution.Version {
        return coordinator.Outcome{Code: "QUEUE_RUN_MISMATCH"}
    }
    if r.Phase == "prepared" && len(r.Request.Args) == 0 {
        return coordinator.Outcome{Code: "QUEUE_DISPATCH_UNCONFIRMED"}
    }
    if !sameExecution(r.Request, queuedExecution(e, "launch")) {
        return coordinator.Outcome{Code: "QUEUE_RUN_MISMATCH"}
    }
    if r.Request.QueueRequestID != e.Request.ID || r.Request.AttemptedAt == nil || e.Request.AttemptedAt == nil || !r.Request.AttemptedAt.Equal(*e.Request.AttemptedAt) {
        return coordinator.Outcome{Code: "QUEUE_RUN_MISMATCH"}
    }
    if r.Status == "unknown" {
        return coordinator.Outcome{Code: "QUEUE_EXECUTION_UNCONFIRMED"}
    }
    return coordinator.Outcome{Accepted: r.SupervisorPID > 0 || wire.Terminal(r.Status), Terminal: wire.Terminal(r.Status)}
}

func queueOwnerLock(root string, create bool) (*os.File, error) {
    flags := os.O_RDWR | unix.O_NOFOLLOW | unix.O_NONBLOCK
    if create {
        flags |= os.O_CREATE
    }
    file, err := os.OpenFile(filepath.Join(root, "coordinator.lock"), flags, 0600)
    if err != nil {
        return nil, err
    }
    var stat unix.Stat_t
    if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&unix.S_IFMT != unix.S_IFREG {
        file.Close()
        return nil, errors.New("QUEUE_UNSAFE_STATE")
    }
    if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        file.Close()
        return nil, err
    }
    return file, nil
}
func coordinatorRunning(root string) bool {
    lock, err := queueOwnerLock(root, false)
    if lock != nil {
        lock.Close()
    }
    return errors.Is(err, unix.EWOULDBLOCK)
}
func startCoordinator(root string) error {
    if coordinatorRunning(root) {
        return nil
    }
    exe, err := os.Executable()
    if err != nil {
        return errors.New("QUEUE_COORDINATOR_UNCONFIRMED")
    }
    null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
    if err != nil {
        return errors.New("QUEUE_COORDINATOR_UNCONFIRMED")
    }
    defer null.Close()
    cmd := exec.Command(exe, "__coordinate", root)
    cmd.Stdin = null
    cmd.Stdout = null
    cmd.Stderr = null
    cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
    if err = cmd.Start(); err != nil {
        return errors.New("QUEUE_COORDINATOR_UNCONFIRMED")
    }
    _ = cmd.Process.Release()
    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        if coordinatorRunning(root) {
            return nil
        }
        time.Sleep(10 * time.Millisecond)
    }
    return errors.New("QUEUE_COORDINATOR_UNCONFIRMED")
}
func queueDiagnostic(root, code string) {
    path := filepath.Join(root, "coordinator.log")
    flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND | unix.O_NOFOLLOW
    if info, err := os.Lstat(path); err == nil && info.Size() > 64<<10 {
        flags = os.O_WRONLY | os.O_TRUNC | unix.O_NOFOLLOW
    }
    file, err := os.OpenFile(path, flags, 0600)
    if err != nil {
        return
    }
    defer file.Close()
    fmt.Fprintln(file, time.Now().UTC().Format(time.RFC3339), code)
}

func cleanQueuedInput(ctx context.Context, e queuestate.Entry, dry bool) error {
    if e.Request.State != "canceled" || e.Attempted {
        return errors.New("QUEUE_NOT_CANCELABLE")
    }
    req := queuedExecution(e, "status")
    if _, err := os.Lstat(req.Root); errors.Is(err, os.ErrNotExist) {
        return nil
    }
    state, err := remotestate.Open(req.Root, false)
    if err != nil {
        return err
    }
    err = state.VerifyProject(req.ProjectID)
    state.Close()
    if err != nil {
        return err
    }
    projectLock, err := lockProject(req.Root)
    if err != nil {
        return err
    }
    defer projectLock.Close()
    dir, err := runPath(req)
    if err != nil {
        if err.Error() == "RUN_NOT_FOUND" {
            return nil
        }
        return err
    }
    dataLock, err := lockData(dir, true)
    if err != nil {
        if err.Error() == "RUN_NOT_FOUND" {
            return nil
        }
        return err
    }
    defer dataLock.Close()
    record, err := read(dir)
    if err != nil {
        return err
    }
    if record.Phase != "prepared" && record.Phase != "copying" || record.SupervisorPID != 0 || record.ChildPID != 0 {
        return errors.New("QUEUE_NOT_CANCELABLE")
    }
    launchLock, err := cleanupLock(filepath.Join(dir, "launch.lock"), !dry)
    if err != nil {
        return err
    }
    defer closeLock(launchLock)
    if dry {
        return nil
    }
    if ctx.Err() != nil {
        return errors.New("QUEUE_CLEAN_UNCONFIRMED")
    }
    root, err := os.OpenRoot(dir)
    if err != nil {
        return errors.New("QUEUE_CLEAN_UNCONFIRMED")
    }
    var locked unix.Stat_t
    info, statErr := root.Stat(".")
    if statErr != nil {
        root.Close()
        return errors.New("CLEAN_UNSAFE")
    }
    opened, ok := info.Sys().(*syscall.Stat_t)
    if unix.Fstat(int(dataLock.Fd()), &locked) != nil || !ok || opened.Ino != locked.Ino || uint64(opened.Dev) != uint64(locked.Dev) {
        root.Close()
        return errors.New("CLEAN_UNSAFE")
    }
    err = removeRunTree(ctx, root, ".")
    root.Close()
    if err != nil {
        return errors.New("QUEUE_CLEAN_UNCONFIRMED")
    }
    if err = os.Remove(dir); err != nil {
        return errors.New("QUEUE_CLEAN_UNCONFIRMED")
    }
    parent, err := os.Open(filepath.Dir(dir))
    if err != nil {
        return errors.New("QUEUE_CLEAN_UNCONFIRMED")
    }
    defer parent.Close()
    if err = parent.Sync(); err != nil {
        return errors.New("QUEUE_CLEAN_UNCONFIRMED")
    }
    return nil
}
