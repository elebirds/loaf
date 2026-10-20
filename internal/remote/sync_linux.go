package remote

import (
    "context"
    "encoding/json"
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

    "github.com/elebirds/loaf/internal/atomicfile"
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
    if args[0] != wire.Version {
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
            req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "launch", Root: root, ProjectID: args[2], RunID: runID}
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
    })
}

func updateCache(root, project string, receive func(string, *os.File) error) error {
    root, err := initProject(root, project)
    if err != nil {
        return err
    }
    lock, err := lockProject(root)
    if err != nil {
        return err
    }
    defer lock.Close()
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
    marker := filepath.Join(root, ".loaf-project")
    if _, err := os.Lstat(marker); errors.Is(err, fs.ErrNotExist) {
        entries, err := os.ReadDir(root)
        if err != nil {
            return "", errors.New("WRITE_FAILED")
        }
        if len(entries) != 0 {
            return "", errors.New("PROJECT_UNOWNED")
        }
        f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
        if err != nil {
            return "", errors.New("PROJECT_BUSY")
        }
        _, writeErr := f.WriteString(project + "\n")
        syncErr := f.Sync()
        closeErr := f.Close()
        if writeErr != nil || syncErr != nil || closeErr != nil {
            return "", errors.New("WRITE_FAILED")
        }
    }
    f, err := os.OpenFile(marker, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if err != nil {
        return "", errors.New("PROJECT_MISMATCH")
    }
    defer f.Close()
    info, err := f.Stat()
    if err != nil || !info.Mode().IsRegular() {
        return "", errors.New("PROJECT_MISMATCH")
    }
    data, err := io.ReadAll(io.LimitReader(f, 1024))
    if err != nil || strings.TrimSpace(string(data)) != project {
        return "", errors.New("PROJECT_MISMATCH")
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
    f, err := os.OpenFile(filepath.Join(root, ".loaf-cache.json"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    // Pre-sync runner users already have owned caches. The new write marker is
    // required for new transfers, not for querying or starting their old caches.
    if errors.Is(err, fs.ErrNotExist) {
        return nil, nil
    }
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    defer f.Close()
    info, err := f.Stat()
    if err != nil || !info.Mode().IsRegular() {
        return nil, errors.New("BAD_FORMAT")
    }
    var state cacheState
    decoder := json.NewDecoder(io.LimitReader(f, 4096))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.FormatVersion != wire.FormatVersion {
        return nil, errors.New("BAD_FORMAT")
    }
    return &state, nil
}

func writeCacheState(root string, complete bool) error {
    data, err := json.Marshal(cacheState{wire.FormatVersion, complete})
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    if err := atomicfile.Write(filepath.Join(root, ".loaf-cache.json"), append(data, '\n')); err != nil {
        return errors.New("WRITE_FAILED")
    }
    return nil
}
