package sync

import (
    "errors"
    "os"
    "path"
    "path/filepath"
    "strings"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/localpath"
    "github.com/elebirds/loaf/internal/wire"
)

func validDestinationPaths(paths []string) bool {
    for _, name := range paths {
        if !wire.WindowsOutputPath(name) {
            return false
        }
    }
    return true
}

func destinationFilesystem(destination string) *wire.OutputFilesystem {
    descriptor := &wire.OutputFilesystem{Name: "windows-ntfs"}
    for {
        info, err := os.Lstat(destination)
        if err == nil {
            if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
                return descriptor
            }
            break
        }
        if !errors.Is(err, os.ErrNotExist) || filepath.Dir(destination) == destination {
            return descriptor
        }
        destination = filepath.Dir(destination)
    }
    probe, err := os.MkdirTemp(destination, ".loaf-link-check-")
    if err != nil {
        return descriptor
    }
    defer os.Remove(probe)
    target, link := filepath.Join(probe, "target"), filepath.Join(probe, "link")
    if err := os.WriteFile(target, nil, 0600); err != nil {
        return descriptor
    }
    defer os.Remove(target)
    if err := os.Symlink("target", link); err != nil {
        return descriptor
    }
    defer os.Remove(link)
    descriptor.Symlinks = true
    return descriptor
}

// Check exact existing spelling component by component. NTFS's usual
// case-insensitivity must not silently overwrite a differently named output.
func validateDestinationNamespace(destination string, selected wire.OutputSelection, filesystem *wire.OutputFilesystem) error {
    unsafe := func() error { return fault.New("UNSAFE_PATH", "pull_destination", nil) }
    if filesystem == nil || selected.Namespace == "" || wire.OutputNamespace(selected.Names) != selected.Namespace {
        return unsafe()
    }
    if filesystem.Symlinks && !destinationFilesystem(destination).Symlinks {
        return unsafe()
    }
    seen := map[string]bool{}
    directories := map[string][]os.DirEntry{}
    for _, entry := range selected.Names {
        if !wire.WindowsOutputPath(entry.Path) || seen[strings.ToUpper(entry.Path)] ||
            entry.Kind != "file" && entry.Kind != "directory" && entry.Kind != "symlink" || entry.Kind == "symlink" && !filesystem.Symlinks {
            return unsafe()
        }
        seen[strings.ToUpper(entry.Path)] = true
        for current := entry.Path; current != "."; current = path.Dir(current) {
            parent := path.Dir(current)
            children, ok := directories[parent]
            if !ok {
                var err error
                children, err = os.ReadDir(filepath.Join(destination, filepath.FromSlash(parent)))
                if err != nil && !errors.Is(err, os.ErrNotExist) {
                    return unsafe()
                }
                directories[parent] = children
            }
            for _, child := range children {
                if strings.EqualFold(child.Name(), path.Base(current)) && child.Name() != path.Base(current) {
                    return unsafe()
                }
            }
            // NTFS short names also alias long names. Reject an existing file
            // reached through an 8.3 alias before rsync could overwrite it.
            candidate := filepath.Join(destination, filepath.FromSlash(current))
            if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink == 0 {
                canonical, err := localpath.Canonical(candidate)
                if err != nil || filepath.Base(canonical) != path.Base(current) {
                    return unsafe()
                }
            } else if err != nil && !errors.Is(err, os.ErrNotExist) {
                return unsafe()
            }
        }
    }
    for _, name := range selected.Available {
        if !seen[strings.ToUpper(name)] {
            return unsafe()
        }
    }
    filesystem.Namespace = selected.Namespace
    return nil
}
