package remote

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
)

func TestPreparedCopyUsesTheReceivingLockAndNeverReadsLaterCache(t *testing.T) {
    root := managedRoot(t)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "launch", Root: root, ProjectID: "project", RunID: "prepared"}
    err := updateCache(root, "project", func(cache string, _ *os.File) error {
        writeSource(t, cache, "source", "first")
        dir, err := runPath(req)
        if err != nil {
            return err
        }
        _, err = copyWork(req, dir)
        if err != nil {
            return err
        }
        _, err = lockProject(root)
        expectCode(t, err, "PROJECT_BUSY")
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }
    writeSource(t, root, "cache/source", "later")
    data, err := os.ReadFile(filepath.Join(root, "runs/prepared/work/source"))
    if err != nil || string(data) != "first" {
        t.Fatal(string(data), err)
    }
    r, err := read(filepath.Join(root, "runs/prepared"))
    if err != nil || r.Phase != "prepared" {
        t.Fatal(r, err)
    }
    _, err = copyWork(req, filepath.Join(root, "runs/prepared"))
    expectCode(t, err, "RUN_EXISTS")
    req.Args = []string{"/bin/true"}
    req.Env = map[string]string{"BAD=KEY": "x"}
    _, err = Handle(req)
    expectCode(t, err, "BAD_ENV")
    r, _ = read(filepath.Join(root, "runs/prepared"))
    if r.Phase != "prepared" {
        t.Fatal("failed validation consumed ready copy")
    }
}
