package remote

import (
    "context"
    "errors"
    "io"
    "io/fs"
    "os"
    "path"
    "path/filepath"
    "strings"

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

// Inventory lists at most Limit entries to Depth below each selected root. It
// holds only the shared payload lock, never follows directory links, and creates
// no project, helper, run or lock files. A running tree is a live observation.
func Inventory(ctx context.Context, req wire.Request) (wire.OutputInventory, error) {
    result := wire.OutputInventory{Entries: []wire.OutputEntry{}, Missing: []string{}, Depth: req.Depth, Limit: req.Limit, RootsTotal: len(req.Outputs)}
    if req.FormatVersion != wire.FormatVersion {
        return result, errors.New("BAD_FORMAT")
    }
    if !wire.CompatibleVersion(wire.Version, req.Version) {
        return result, errors.New("VERSION_MISMATCH")
    }
    if req.Depth < 0 || req.Depth > 20 || req.Limit < 1 || req.Limit > 1000 || len(req.Outputs) == 0 || len(req.Outputs) > 1000 {
        return result, errors.New("BAD_REQUEST")
    }
    for _, name := range req.Outputs {
        if !wire.ValidOutputPath(name) {
            return result, &outputError{"UNSAFE_PATH", name}
        }
    }
    req.Action = "status"
    dir, err := runPath(req)
    if err != nil {
        return result, err
    }
    lock, err := lockData(dir, false)
    if err != nil {
        return result, err
    }
    defer lock.Close()
    if err := cleanedAvailability(req, dir); err != nil {
        return result, err
    }
    record, err := read(dir)
    if err != nil {
        return result, err
    }
    if record.FormatVersion != wire.FormatVersion || record.Request.FormatVersion != wire.FormatVersion || record.Request.ProjectID != req.ProjectID || record.Request.RunID != req.RunID || record.Request.Root != req.Root {
        return result, errors.New("BAD_FORMAT")
    }
    work := filepath.Join(dir, "work")
    real, err := filepath.EvalSymlinks(work)
    if err != nil || real != work {
        return result, &outputError{"UNSAFE_PATH", "work"}
    }
    root, err := os.OpenRoot(work)
    if err != nil {
        return result, &outputError{"OUTPUT_READ_FAILED", "work"}
    }
    defer root.Close()
    seen := map[string]bool{}
    var visit func(string, int) error
    visit = func(name string, depth int) error {
        if ctx.Err() != nil {
            return errors.New("OBSERVATION_CANCELED")
        }
        if seen[name] {
            return nil
        }
        if len(result.Entries) >= req.Limit {
            result.Truncated = true
            return nil
        }
        info, err := root.Lstat(name)
        if err != nil {
            return &outputError{"OUTPUT_READ_FAILED", name}
        }
        kind := "file"
        switch {
        case info.IsDir():
            kind = "directory"
        case info.Mode()&os.ModeSymlink != 0:
            kind = "symlink"
            target, linkErr := root.Readlink(name)
            resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(work, filepath.FromSlash(name)))
            rel, relErr := filepath.Rel(work, resolved)
            if linkErr != nil || filepath.IsAbs(target) || resolveErr != nil || relErr != nil || !withinOutputs(filepath.ToSlash(rel), req.Outputs) {
                return &outputError{"UNSAFE_PATH", name}
            }
            if _, err := root.Stat(name); err != nil {
                return &outputError{"UNSAFE_PATH", name}
            }
        case !info.Mode().IsRegular():
            return &outputError{"UNSAFE_PATH", name}
        }
        seen[name] = true
        result.Entries = append(result.Entries, wire.OutputEntry{Path: name, Type: kind, Size: info.Size(), ModifiedAt: info.ModTime().UTC()})
        result.Examined++
        if !info.IsDir() {
            return nil
        }
        if depth >= req.Depth {
            result.DepthLimited++
            result.Truncated = true
            return nil
        }
        // O_NOFOLLOW closes a directory-replacement race between Lstat and Open.
        directory, err := root.OpenFile(name, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
        if err != nil {
            return &outputError{"OUTPUT_READ_FAILED", name}
        }
        defer directory.Close()
        for {
            // A one-entry lookahead establishes truncation without reading an
            // unbounded directory into memory or traversing omitted subtrees.
            entries, err := directory.ReadDir(1)
            if err != nil && err != io.EOF {
                return &outputError{"OUTPUT_READ_FAILED", name}
            }
            if len(entries) == 0 {
                return nil
            }
            if len(result.Entries) >= req.Limit {
                result.Truncated = true
                return nil
            }
            if err := visit(path.Join(name, entries[0].Name()), depth+1); err != nil {
                return err
            }
        }
    }
    for _, name := range req.Outputs {
        if ctx.Err() != nil {
            return result, errors.New("OBSERVATION_CANCELED")
        }
        if len(result.Entries) >= req.Limit {
            result.Truncated = true
            break
        }
        // Explicit roots must not traverse parent directory links either.
        parent := ""
        parts := strings.Split(name, "/")
        for _, part := range parts[:len(parts)-1] {
            parent = path.Join(parent, part)
            info, err := root.Lstat(parent)
            if err == nil && !info.IsDir() {
                return result, &outputError{"UNSAFE_PATH", parent}
            }
            if err != nil && !errors.Is(err, fs.ErrNotExist) {
                return result, &outputError{"OUTPUT_READ_FAILED", parent}
            }
        }
        result.RootsExamined++
        if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
            result.Missing = append(result.Missing, name)
            continue
        } else if err != nil {
            return result, &outputError{"OUTPUT_READ_FAILED", name}
        }
        if err := visit(name, 0); err != nil {
            return result, err
        }
    }
    return result, nil
}
