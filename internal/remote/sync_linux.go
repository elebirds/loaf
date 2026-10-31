package remote

import (
    "context"
    "errors"
    "fmt"
    "io"
    "io/fs"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "slices"
    "strings"
    "syscall"

    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

type cacheState struct {
    FormatVersion int  `json:"format_version"`
    Complete      bool `json:"complete"`
}

// Receive is the rsync-path wrapper. stdin/stdout belong entirely to rsync;
// only fixed project context is passed in argv, never in the transport stream.
func Receive(in io.Reader, out, diagnostic io.Writer, args []string) int {
    err := receive(in, out, diagnostic, args)
    if err != nil {
        fmt.Fprintf(diagnostic, "LOAF_SYNC_ERROR:%s\n", err.Error())
        return 1
    }
    return 0
}

func receive(in io.Reader, out, diagnostic io.Writer, args []string) error {
    if len(args) < 7 {
        return errors.New("BAD_RECEIVER")
    }
    if !wire.CompatibleVersion(wire.Version, args[0]) {
        return errors.New("VERSION_MISMATCH")
    }
    runID := ""
    native := args[3:]
    if len(native) >= 2 && native[0] == "--run" {
        runID = native[1]
        if !identifier.MatchString(runID) {
            return errors.New("BAD_RECEIVER")
        }
        native = native[2:]
    }
    if len(native) < 4 {
        return errors.New("BAD_RECEIVER")
    }
    argv := append([]string(nil), native...)
    // The wrapper accepts receiving mode at one fixed destination. Other native
    // rsync options are passed transparently, not parsed or version-whitelisted.
    if argv[0] != "--server" || slices.Contains(argv, "--sender") || argv[len(argv)-2] != "." || argv[len(argv)-1] != "cache" {
        return errors.New("BAD_RECEIVER")
    }
    return updateCache(args[1], args[2], func(cache string, lock *os.File) error {
        ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
        defer cancel()
        argv[len(argv)-1] = cache + "/"
        cmd := exec.CommandContext(ctx, "rsync", argv...)
        cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, diagnostic
        cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
        // Retain ownership until the receiver exits, even if this wrapper dies.
        cmd.ExtraFiles = []*os.File{lock}
        cmd.Cancel = func() error {
            err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
            if errors.Is(err, syscall.ESRCH) {
                return os.ErrProcessDone
            }
            return err
        }
        if err := cmd.Run(); err != nil {
            return errors.New("TRANSFER_FAILED")
        }
        if ctx.Err() != nil {
            return errors.New("TRANSFER_FAILED")
        }
        if runID != "" {
            root := filepath.Dir(cache)
            req := wire.Request{FormatVersion: wire.FormatVersion, Version: args[0], Action: "launch", Root: root, ProjectID: args[2], RunID: runID}
            dir, err := runPath(req)
            if err != nil {
                return err
            }
            _, err = copyWork(req, dir)
            if err != nil {
                return err
            }
        }
        if ctx.Err() != nil {
            return errors.New("TRANSFER_FAILED")
        }
        return nil
    }, func(root string) error {
        if runID == "" {
            return nil
        }
        return preparationAvailability(wire.Request{Root: root, ProjectID: args[2], RunID: runID})
    })
}

func updateCache(root, project string, receive func(string, *os.File) error, guards ...func(string) error) error {
    root, err := initProject(root, project)
    if err != nil {
        return err
    }
    lock, err := lockProject(root)
    if err != nil {
        return err
    }
    defer lock.Close()
    for _, guard := range guards {
        if err := guard(root); err != nil {
            return err
        }
    }
    if _, err := loadCacheState(root); err != nil {
        return err
    }
    cache := filepath.Join(root, "cache")
    if err := os.Mkdir(cache, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
        return errors.New("WRITE_FAILED")
    }
    info, err := os.Lstat(cache)
    if err != nil || !info.IsDir() {
        return errors.New("UNSAFE_PATH")
    }
    if err := writeCacheState(root, false); err != nil {
        return err
    }
    if err := receive(cache, lock); err != nil {
        return err
    }
    return writeCacheState(root, true)
}

func initProject(root, project string) (string, error) {
    if !identifier.MatchString(project) || strings.ContainsAny(root, "\x00\r\n") {
        return "", errors.New("UNSAFE_PATH")
    }
    home, err := os.UserHomeDir()
    if err != nil {
        return "", errors.New("UNSAFE_PATH")
    }
    if strings.HasPrefix(root, "~/") {
        root = filepath.Join(home, root[2:])
    }
    if !filepath.IsAbs(root) {
        return "", errors.New("UNSAFE_PATH")
    }
    for _, part := range strings.Split(root, "/") {
        if part == ".." {
            return "", errors.New("UNSAFE_PATH")
        }
    }
    root = filepath.Clean(root)
    if root == "/" || root == home {
        return "", errors.New("UNSAFE_PATH")
    }
    // Reject symlinked parents before creating anything at the requested path.
    for dir := root; dir != "/"; dir = filepath.Dir(dir) {
        info, err := os.Lstat(dir)
        if err == nil && !info.IsDir() {
            return "", errors.New("UNSAFE_PATH")
        }
        if err != nil && !errors.Is(err, fs.ErrNotExist) {
            return "", errors.New("WRITE_FAILED")
        }
    }
    if err := os.MkdirAll(root, 0700); err != nil {
        return "", errors.New("WRITE_FAILED")
    }
    if err := remotestate.Initialize(root, project); err != nil {
        return "", err
    }
    return root, nil
}

func lockProject(root string) (*os.File, error) {
    f, err := os.OpenFile(filepath.Join(root, ".loaf-project.lock"), os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
    if err != nil {
        return nil, errors.New("WRITE_FAILED")
    }
    info, err := f.Stat()
    if err != nil || !info.Mode().IsRegular() {
        f.Close()
        return nil, errors.New("UNSAFE_PATH")
    }
    if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        f.Close()
        if errors.Is(err, unix.EWOULDBLOCK) {
            return nil, errors.New("PROJECT_BUSY")
        }
        return nil, errors.New("WRITE_FAILED")
    }
    return f, nil
}

func loadCacheState(root string) (*cacheState, error) {
    state, err := remotestate.Open(root, false)
    if err != nil {
        return nil, err
    }
    defer state.Close()
    complete, err := state.Sync()
    if err != nil || complete == nil {
        return nil, err
    }
    return &cacheState{wire.FormatVersion, *complete}, nil
}

func writeCacheState(root string, complete bool) error {
    state, err := remotestate.Open(root, true)
    if err != nil {
        return err
    }
    defer state.Close()
    return state.SetSync(complete)
}
