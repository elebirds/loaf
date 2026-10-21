package sync

import (
    "context"
    "errors"
    "io/fs"
    "os"
    "path/filepath"
    "slices"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

// OutputRemote is the read-only SSH seam needed to retrieve an existing run.
type OutputRemote interface {
    RunnerArch(context.Context, config.Target, bool) (string, error)
    Outputs(context.Context, config.Target, string, wire.Request, bool) (wire.OutputSelection, error)
}

// Downloader performs a native rsync pull into a validated local directory.
type Downloader interface {
    Pull(context.Context, config.Target, string, string, ssh.PullInput, bool) (ssh.Transfer, error)
}

// PullService reuses saved run locations, not today's target or output settings.
type PullService struct {
    Remote  OutputRemote
    Runner  func(context.Context, config.Target, string, bool) (string, error)
    Receive Downloader
}

// PullResult keeps partial selections visible without claiming failed transfers
// completed. ChangedFiles is present only when native rsync returned success.
type PullResult struct {
    RunID           string            `json:"run_id"`
    Target          string            `json:"target"`
    Destination     string            `json:"destination"`
    Paths           []string          `json:"paths"`
    DownloadedPaths []string          `json:"downloaded_paths"`
    MissingPaths    []string          `json:"missing_paths"`
    ChangedFiles    *int              `json:"changed_files,omitempty"`
    Policy          config.PullPolicy `json:"policy"`
}

// OutputPaths normalizes optional trailing slashes and validates work-relative
// subpaths. Metacharacters remain literal filename characters.
func OutputPaths(paths []string) ([]string, error) {
    result := []string{}
    for _, name := range paths {
        name = strings.TrimSuffix(name, "/")
        if !wire.ValidOutputPath(name) {
            return nil, fault.New("BAD_CONFIG", "pull_paths", nil)
        }
        if !slices.Contains(result, name) {
            result = append(result, name)
        }
    }
    if len(result) == 0 {
        return nil, fault.New("BAD_CONFIG", "pull_paths", nil)
    }
    return result, nil
}

// Pull downloads available selected outputs and reports missing selections as a
// partial failure. It never waits for training or mutates its records/locator.
func (s PullService) Pull(ctx context.Context, ref config.RunRef, destination string, paths []string, policy config.PullPolicy, batch bool) (result PullResult, err error) {
    result = PullResult{RunID: ref.ID, Target: ref.Target, Destination: destination, Paths: []string{}, DownloadedPaths: []string{}, MissingPaths: []string{}, Policy: policy}
    if ref.Version != wire.Version {
        f := fault.New("VERSION_MISMATCH", "runner_version_mismatch", map[string]any{"Expected": wire.Version, "Actual": ref.Version})
        f.Hint = "runner_version_hint"
        return result, f
    }
    defer func() {
        if err != nil && ctx.Err() != nil {
            f := fault.Wrap("PULL_CANCELED", "pull_canceled", ctx.Err())
            f.Hint = "pull_retry"
            err = f
        }
    }()
    if ref.Cleanup != nil {
        code, message := "CLEAN_UNCONFIRMED", "clean_unconfirmed"
        if ref.Cleanup.CleanedAt != nil {
            code, message = "RUN_CLEANED", "clean_run_cleaned"
        }
        f := fault.New(code, message, nil)
        if ref.Cleanup.CleanedAt == nil {
            f.Hint = "clean_retry"
        }
        return result, f
    }
    result.Paths, err = OutputPaths(paths)
    if err != nil {
        return result, err
    }
    preflight, cancel := context.WithTimeout(ctx, 30*time.Second)
    arch, err := s.Remote.RunnerArch(preflight, ref.Connection, batch)
    var runner string
    if err == nil {
        runner, err = s.Runner(preflight, ref.Connection, arch, batch)
    }
    cancel()
    if err != nil {
        return result, err
    }
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "outputs", Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, Outputs: result.Paths}
    selected, err := s.Remote.Outputs(ctx, ref.Connection, runner, req, batch)
    if err != nil {
        return result, err
    }
    if !validOutputSelection(result.Paths, selected) {
        return result, fault.New("BAD_RESPONSE", "pull_format", nil)
    }
    result.MissingPaths = append(result.MissingPaths, selected.Missing...)
    if len(selected.Available) > 0 {
        lock, err := openDestination(destination, selected.Available)
        if err != nil {
            return result, err
        }
        defer lock.Close()
        // No persistent download state or per-run remote execution lock is used.
        transferred, err := s.Receive.Pull(ctx, ref.Connection, runner, destination, ssh.PullInput{Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, Paths: selected.Available, Checksum: policy.Checksum}, batch)
        if err != nil {
            return result, err
        }
        result.DownloadedPaths = append(result.DownloadedPaths, selected.Available...)
        result.ChangedFiles = &transferred.ChangedFiles
    }
    if len(selected.Missing) > 0 {
        f := fault.New("OUTPUT_MISSING", "pull_missing", nil)
        f.Hint = "pull_missing_hint"
        return result, f
    }
    return result, nil
}

func validOutputSelection(paths []string, selected wire.OutputSelection) bool {
    seen := map[string]bool{}
    for _, group := range [][]string{selected.Available, selected.Missing} {
        for _, name := range group {
            if !slices.Contains(paths, name) || seen[name] {
                return false
            }
            seen[name] = true
        }
    }
    return len(seen) == len(paths)
}

// Destination resolves an explicit --to against cwd; defaults append the run ID
// under the project-relative configured base. Existing outer parents are
// canonicalized (including macOS /tmp), but the destination itself is not a link.
func Destination(project, cwd, to, id string, policy config.PullPolicy) (string, error) {
    destination := to
    if destination == "" {
        base, err := config.PullBase(project, policy)
        if err != nil {
            return "", err
        }
        destination = filepath.Join(base, id)
    } else if !filepath.IsAbs(destination) {
        destination = filepath.Join(cwd, destination)
    }
    if strings.ContainsAny(destination, "\x00\r\n") {
        return "", fault.New("BAD_CONFIG", "pull_destination", nil)
    }
    destination = filepath.Clean(destination)
    info, err := os.Lstat(destination)
    if err == nil && !info.IsDir() || err != nil && !errors.Is(err, fs.ErrNotExist) {
        return "", fault.New("UNSAFE_PATH", "pull_destination", nil)
    }
    // Resolve the nearest existing parent without creating the destination.
    parent := filepath.Dir(destination)
    suffix := filepath.Base(destination)
    for {
        real, err := filepath.EvalSymlinks(parent)
        if err == nil {
            destination = filepath.Join(real, suffix)
            break
        }
        if !errors.Is(err, fs.ErrNotExist) || parent == filepath.Dir(parent) {
            return "", fault.Wrap("UNSAFE_PATH", "pull_destination", err)
        }
        suffix = filepath.Join(filepath.Base(parent), suffix)
        parent = filepath.Dir(parent)
    }
    home, _ := os.UserHomeDir()
    if actual, err := filepath.EvalSymlinks(home); err == nil {
        home = actual
    }
    if destination == home {
        return "", fault.New("UNSAFE_PATH", "pull_destination", nil)
    }
    if rel, err := filepath.Rel(destination, project); err != nil || filepath.IsLocal(rel) {
        return "", fault.New("UNSAFE_PATH", "pull_destination", nil)
    }
    return destination, nil
}

func openDestination(destination string, paths []string) (*os.File, error) {
    failure := func(err error) (*os.File, error) {
        f := fault.Wrap("PULL_FAILED", "pull_write", err)
        f.Hint = "pull_retry"
        f.Params = map[string]any{"Detail": err.Error()}
        return nil, f
    }
    if err := os.MkdirAll(destination, 0700); err != nil {
        return failure(err)
    }
    real, err := filepath.EvalSymlinks(destination)
    if err != nil || real != destination {
        return nil, fault.New("UNSAFE_PATH", "pull_destination", nil)
    }
    lock, err := os.OpenFile(destination, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
    if err != nil {
        return failure(err)
    }
    if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        lock.Close()
        if errors.Is(err, unix.EWOULDBLOCK) {
            return nil, fault.New("PULL_BUSY", "pull_busy", nil)
        }
        return failure(err)
    }
    if err := checkDestination(destination, paths); err != nil {
        lock.Close()
        return nil, err
    }
    return lock, nil
}

func checkDestination(destination string, paths []string) error {
    for _, name := range paths {
        for parent := filepath.Dir(filepath.FromSlash(name)); parent != "."; parent = filepath.Dir(parent) {
            info, err := os.Lstat(filepath.Join(destination, parent))
            if err == nil && !info.IsDir() {
                return fault.New("UNSAFE_PATH", "pull_destination", nil)
            }
            if err != nil && !errors.Is(err, fs.ErrNotExist) {
                return fault.Wrap("PULL_FAILED", "pull_write", err)
            }
        }
        err := filepath.WalkDir(filepath.Join(destination, filepath.FromSlash(name)), func(name string, entry fs.DirEntry, err error) error {
            if errors.Is(err, fs.ErrNotExist) {
                return nil
            }
            if err != nil {
                return fault.Wrap("PULL_FAILED", "pull_write", err)
            }
            if entry.Type()&os.ModeSymlink != 0 {
                resolved, err := filepath.EvalSymlinks(name)
                rel, relErr := filepath.Rel(destination, resolved)
                if err != nil || relErr != nil || !filepath.IsLocal(rel) || rel == "." {
                    return fault.New("UNSAFE_PATH", "pull_destination", nil)
                }
            } else if !entry.IsDir() && !entry.Type().IsRegular() {
                return fault.New("UNSAFE_PATH", "pull_destination", nil)
            }
            return nil
        })
        if err != nil {
            return err
        }
    }
    return nil
}
