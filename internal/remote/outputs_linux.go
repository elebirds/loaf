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
    "path"
    "path/filepath"
    "slices"
    "strings"
    "syscall"

    "github.com/elebirds/loaf/internal/wire"
)

type outputError struct{ code, path string }

func (e *outputError) Error() string { return e.code }

// Outputs validates a saved run and all selected trees without changing records,
// requiring a live supervisor, or acquiring execution/project locks.
func Outputs(ctx context.Context, req wire.Request) (wire.OutputSelection, error) {
    selection := wire.OutputSelection{Available: []string{}, Missing: []string{}}
    if req.FormatVersion != wire.FormatVersion {
        return selection, errors.New("BAD_FORMAT")
    }
    if req.Version != wire.Version {
        return selection, errors.New("VERSION_MISMATCH")
    }
    if len(req.Outputs) == 0 {
        return selection, errors.New("BAD_REQUEST")
    }
    if req.DestinationFS != nil && req.DestinationFS.Name != "windows-ntfs" {
        return selection, errors.New("BAD_REQUEST")
    }
    for _, name := range req.Outputs {
        if !wire.ValidOutputPath(name) {
            return selection, &outputError{"UNSAFE_PATH", name}
        }
    }
    // Reuse the existing read-only ownership check. Do not let a new action
    // accidentally create runs/ while inspecting a missing or damaged project.
    req.Action = "status"
    dir, err := runPath(req)
    if err != nil {
        return selection, err
    }
    if err := cleanedAvailability(req, dir); err != nil {
        return selection, err
    }
    record, err := read(dir)
    if err != nil {
        return selection, err
    }
    if record.Request.FormatVersion != wire.FormatVersion || record.Request.ProjectID != req.ProjectID || record.Request.RunID != req.RunID || record.Request.Root != req.Root {
        return selection, errors.New("BAD_FORMAT")
    }
    work := filepath.Join(dir, "work")
    real, err := filepath.EvalSymlinks(work)
    if err != nil || real != work {
        return selection, &outputError{"UNSAFE_PATH", "work"}
    }
    root, err := os.OpenRoot(work)
    if err != nil {
        return selection, &outputError{"OUTPUT_READ_FAILED", "work"}
    }
    defer root.Close()
    for _, name := range req.Outputs {
        // No selection may traverse a directory link, even if that link points
        // inside work. rsync's implied-directory handling must remain literal.
        parent := ""
        parts := strings.Split(name, "/")
        for _, part := range parts[:len(parts)-1] {
            parent = path.Join(parent, part)
            info, err := root.Lstat(parent)
            if err == nil && !info.IsDir() {
                return selection, &outputError{"UNSAFE_PATH", parent}
            }
            if err != nil && !errors.Is(err, fs.ErrNotExist) {
                return selection, &outputError{"OUTPUT_READ_FAILED", parent}
            }
        }
        if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
            selection.Missing = append(selection.Missing, name)
        } else if err != nil {
            return selection, &outputError{"OUTPUT_READ_FAILED", name}
        } else {
            selection.Available = append(selection.Available, name)
        }
    }
    names := map[string]wire.OutputName{}
    checkName := func(name, kind string) error {
        if req.DestinationFS == nil {
            return nil
        }
        if !wire.WindowsOutputPath(name) {
            return &outputError{"UNSAFE_PATH", name}
        }
        key := strings.ToUpper(name)
        if prior, exists := names[key]; exists && (prior.Path != name || prior.Kind != kind) {
            return &outputError{"UNSAFE_PATH", name}
        }
        names[key] = wire.OutputName{Path: name, Kind: kind}
        return nil
    }
    for _, name := range selection.Available {
        for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
            if err := checkName(parent, "directory"); err != nil {
                return selection, err
            }
        }
    }
    visit := func(name string, entry fs.DirEntry, err error) error {
        if ctx.Err() != nil {
            return errors.New("PULL_CANCELED")
        }
        if err != nil {
            return &outputError{"OUTPUT_READ_FAILED", name}
        }
        kind := "file"
        if entry.IsDir() {
            kind = "directory"
        } else if entry.Type()&os.ModeSymlink != 0 {
            kind = "symlink"
        }
        if err := checkName(name, kind); err != nil {
            return err
        }
        if entry.IsDir() {
            return nil
        }
        if entry.Type()&os.ModeSymlink != 0 {
            if req.DestinationFS != nil && !req.DestinationFS.Symlinks {
                return &outputError{"UNSAFE_PATH", name}
            }
            target, err := root.Readlink(name)
            resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(work, filepath.FromSlash(name)))
            rel, relErr := filepath.Rel(work, resolved)
            if err != nil || filepath.IsAbs(target) || resolveErr != nil || relErr != nil || !withinOutputs(filepath.ToSlash(rel), selection.Available) {
                return &outputError{"UNSAFE_PATH", name}
            }
            if req.DestinationFS != nil && (strings.Contains(target, `\`) || !wire.WindowsOutputPath(filepath.ToSlash(rel))) {
                return &outputError{"UNSAFE_PATH", name}
            }
            // Root.Stat also confines every link in a chain to work.
            if _, err := root.Stat(name); err != nil {
                return &outputError{"UNSAFE_PATH", name}
            }
            return nil
        }
        if !entry.Type().IsRegular() {
            return &outputError{"UNSAFE_PATH", name}
        }
        return nil
    }
    for _, name := range selection.Available {
        // fs.WalkDir follows its root argument via fs.Stat. Inspect that root
        // with Lstat first so a selected link receives the same checks as a
        // link encountered inside a selected directory.
        info, err := root.Lstat(name)
        if err != nil {
            return selection, &outputError{"OUTPUT_READ_FAILED", name}
        }
        if info.Mode()&os.ModeSymlink != 0 {
            err = visit(name, fs.FileInfoToDirEntry(info), nil)
        } else {
            err = fs.WalkDir(root.FS(), name, visit)
        }
        if err != nil {
            return selection, err
        }
    }
    if req.DestinationFS != nil {
        for _, name := range names {
            selection.Names = append(selection.Names, name)
        }
        slices.SortFunc(selection.Names, func(a, b wire.OutputName) int { return strings.Compare(a.Path, b.Path) })
        selection.Namespace = wire.OutputNamespace(selection.Names)
        if expected := req.DestinationFS.Namespace; expected != "" && expected != selection.Namespace {
            return selection, errors.New("UNSAFE_PATH")
        }
    }
    return selection, nil
}

func withinOutputs(name string, selected []string) bool {
    if !wire.ValidOutputPath(name) {
        return false
    }
    return slices.ContainsFunc(selected, func(root string) bool { return name == root || strings.HasPrefix(name, root+"/") })
}

// SendOutputs is a native rsync-path sender. Context belongs in argv; no Loaf
// framing is added to stdin/stdout, and native server options stay opaque.
func SendOutputs(in io.Reader, out, diagnostic io.Writer, args []string) int {
    err := sendOutputs(in, out, diagnostic, args)
    if err == nil {
        return 0
    }
    fmt.Fprintf(diagnostic, "\nLOAF_PULL_ERROR:%s\n", err.Error())
    var pathErr *outputError
    if errors.As(err, &pathErr) {
        fmt.Fprintf(diagnostic, "Path: %q\n", pathErr.path)
    }
    return 1
}

func sendOutputs(in io.Reader, out, diagnostic io.Writer, args []string) error {
    if len(args) < 9 {
        return errors.New("BAD_SENDER")
    }
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: args[0], Root: args[1], ProjectID: args[2], RunID: args[3]}
    if json.Unmarshal([]byte(args[4]), &req.Outputs) != nil {
        return errors.New("BAD_SENDER")
    }
    native := args[5:]
    if strings.HasPrefix(native[0], "--destination-filesystem=") {
        req.DestinationFS = &wire.OutputFilesystem{}
        if json.Unmarshal([]byte(strings.TrimPrefix(native[0], "--destination-filesystem=")), req.DestinationFS) != nil {
            return errors.New("BAD_SENDER")
        }
        native = native[1:]
    }
    if len(native) < 4 {
        return errors.New("BAD_SENDER")
    }
    if native[0] != "--server" || native[1] != "--sender" || native[len(native)-2] != "." || native[len(native)-1] != "." {
        return errors.New("BAD_SENDER")
    }
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
    defer cancel()
    req.Action = "status"
    dir, err := runPath(req)
    if err != nil {
        return err
    }
    dataLock, err := lockData(dir, false)
    if err != nil {
        if availability := cleanedAvailability(req, dir); availability != nil {
            return availability
        }
        return err
    }
    defer dataLock.Close()
    selection, err := Outputs(ctx, req)
    if err != nil {
        return err
    }
    if len(selection.Missing) > 0 {
        return errors.New("OUTPUT_MISSING")
    }
    cmd := exec.CommandContext(ctx, "rsync", native...)
    cmd.Dir = filepath.Join(req.Root, "runs", req.RunID, "work")
    cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, diagnostic
    // If the wrapper dies first, native rsync retains the shared data lock.
    cmd.ExtraFiles = []*os.File{dataLock}
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
    cmd.Cancel = func() error {
        err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
        if errors.Is(err, syscall.ESRCH) {
            return os.ErrProcessDone
        }
        return err
    }
    if err := cmd.Run(); err != nil {
        return errors.New("PULL_FAILED")
    }
    return nil
}
