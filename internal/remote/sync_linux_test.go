package remote

import (
    "bytes"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
)

func expectCode(t *testing.T, err error, code string) {
    t.Helper()
    if err == nil || err.Error() != code {
        t.Fatalf("want %s, got %v", code, err)
    }
}

func writeSource(t *testing.T, root, name, text string) {
    t.Helper()
    file := filepath.Join(root, name)
    if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(file, []byte(text), 0600); err != nil {
        t.Fatal(err)
    }
}

func TestInitializeOnlyOwnedOrEmptyDirectories(t *testing.T) {
    parent := t.TempDir()
    root := filepath.Join(parent, "new", "项目 space")
    got, err := initProject(root, "project")
    if err != nil || got != root {
        t.Fatal(got, err)
    }
    info, err := os.Stat(filepath.Join(root, ".loaf-project"))
    if err != nil || info.Mode().Perm() != 0600 {
        t.Fatal(info, err)
    }
    if _, err := initProject(root, "project"); err != nil {
        t.Fatal(err)
    }
    _, err = initProject(root, "other")
    expectCode(t, err, "PROJECT_MISMATCH")
    foreign := filepath.Join(parent, "existing")
    writeSource(t, foreign, "research.py", "do not delete")
    _, err = initProject(foreign, "project")
    expectCode(t, err, "PROJECT_UNOWNED")
    if _, err := os.Stat(filepath.Join(foreign, ".loaf-project")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("claimed a foreign directory")
    }
    home, _ := os.UserHomeDir()
    for _, path := range []string{"/", home, parent + "/../escape", "relative", parent + "/bad\nname"} {
        _, err := initProject(path, "project")
        expectCode(t, err, "UNSAFE_PATH")
    }
    link := filepath.Join(parent, "linked")
    if err := os.Symlink(foreign, link); err != nil {
        t.Fatal(err)
    }
    _, err = initProject(filepath.Join(link, "child"), "project")
    expectCode(t, err, "UNSAFE_PATH")
    if _, err := os.Stat(filepath.Join(foreign, "child")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("wrote through a symlinked parent")
    }
}

func TestCacheFailureBlocksNewStartsAndCanBeRepaired(t *testing.T) {
    root := managedRoot(t)
    err := updateCache(root, "project", func(cache string, _ *os.File) error {
        writeSource(t, cache, "train.py", "half written")
        return errors.New("TRANSFER_FAILED")
    })
    expectCode(t, err, "TRANSFER_FAILED")
    state, err := loadCacheState(root)
    if err != nil || state == nil || state.Complete {
        t.Fatal(state, err)
    }
    startRequest := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "start", Root: root, ProjectID: "project", RunID: "blocked", Args: []string{"/bin/true"}}
    _, err = Handle(startRequest)
    expectCode(t, err, "SYNC_INCOMPLETE")
    if _, err := os.Stat(filepath.Join(root, "runs", "blocked")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("created a run from an incomplete source")
    }
    err = updateCache(root, "project", func(cache string, _ *os.File) error {
        writeSource(t, cache, "train.py", "complete")
        return nil
    })
    state, stateErr := loadCacheState(root)
    if err != nil || stateErr != nil || state == nil || !state.Complete {
        t.Fatal(state, err, stateErr)
    }
}

func TestCacheLockCoversReceptionAndSourceCopyEntry(t *testing.T) {
    root := managedRoot(t)
    err := updateCache(root, "project", func(cache string, _ *os.File) error {
        // Both another transfer and an internal start contend on this same lock.
        expectCode(t, updateCache(root, "project", func(string, *os.File) error { t.Fatal("concurrent receiver entered"); return nil }), "PROJECT_BUSY")
        _, err := Handle(wire.Request{FormatVersion: 1, Version: wire.Version, Action: "start", Root: root, ProjectID: "project", RunID: "concurrent", Args: []string{"/bin/true"}})
        expectCode(t, err, "PROJECT_BUSY")
        writeSource(t, cache, "train.py", "complete")
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }
    lock, err := lockProject(root)
    if err != nil {
        t.Fatal("lock not released", err)
    }
    lock.Close()
}

func TestUnrecognizedCacheFormatIsPreserved(t *testing.T) {
    root := managedRoot(t)
    original := `{"format_version":99,"complete":true}`
    writeSource(t, root, ".loaf-cache.json", original)
    expectCode(t, updateCache(root, "project", func(string, *os.File) error { t.Fatal("ignored an unknown format"); return nil }), "BAD_FORMAT")
    data, err := os.ReadFile(filepath.Join(root, ".loaf-cache.json"))
    if err != nil || string(data) != original {
        t.Fatal(string(data), err)
    }
}

func TestReceiverChecksContextAndDestinationWithoutReadingTheStream(t *testing.T) {
    root := managedRoot(t)
    for _, args := range [][]string{
        {}, {wire.Version, root, "project", "--server", "--sender", "-rlptc", ".", "cache"},
        {wire.Version, root, "project", "--server", "-rlptc", ".", "../elsewhere"},
        {"other-version", root, "project", "--server", "-rlptc", ".", "cache"},
    } {
        var out, diagnostic bytes.Buffer
        if Receive(strings.NewReader("opaque rsync bytes"), &out, &diagnostic, args) != 1 || out.Len() != 0 || !strings.Contains(diagnostic.String(), "LOAF_SYNC_ERROR:") {
            t.Fatal(args, out.String(), diagnostic.String())
        }
    }
}

func TestCacheDirectoryCannotBeASymlink(t *testing.T) {
    root := managedRoot(t)
    if err := os.RemoveAll(filepath.Join(root, "cache")); err != nil {
        t.Fatal(err)
    }
    outside := t.TempDir()
    if err := os.Symlink(outside, filepath.Join(root, "cache")); err != nil {
        t.Fatal(err)
    }
    expectCode(t, updateCache(root, "project", func(string, *os.File) error { t.Fatal("followed cache symlink"); return nil }), "UNSAFE_PATH")
}
