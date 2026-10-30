package remote

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/event"
    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

func abandonFixture(t *testing.T) (wire.Request, string) {
    t.Helper()
    root := managedRoot(t)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "launch", Root: root, ProjectID: "project", RunID: "preparation"}
    err := updateCache(root, req.ProjectID, func(cache string, _ *os.File) error {
        writeSource(t, cache, "input", "shared")
        dir, err := runPath(req)
        if err != nil {
            return err
        }
        _, err = copyWork(req, dir)
        return err
    })
    if err != nil {
        t.Fatal(err)
    }
    return req, filepath.Join(root, "runs", req.RunID)
}

func TestAbandonPreviewAndPreparedStartingStatus(t *testing.T) {
    req, dir := abandonFixture(t)
    record, _ := read(dir)
    if record.Status != "starting" || record.Phase != "prepared" {
        t.Fatal(record)
    }
    before := treeContents(t, req.Root)
    preview, err := Abandon(context.Background(), req, true)
    if err != nil || preview.NoStartAt != nil || !preview.PayloadPresent || !reflect.DeepEqual(before, treeContents(t, req.Root)) {
        t.Fatal(preview, err)
    }
    writeSource(t, req.Root, "runs/neighbor/sentinel", "keep")
    receipt, err := Abandon(context.Background(), req, false)
    if err != nil || receipt.NoStartAt == nil || receipt.CleanedAt == nil {
        t.Fatal(receipt, err)
    }
    if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
        t.Fatal(err)
    }
    for _, name := range []string{"cache/input", "runs/neighbor/sentinel"} {
        if _, err := os.Stat(filepath.Join(req.Root, name)); err != nil {
            t.Fatal(err)
        }
    }
    again, err := Abandon(context.Background(), req, false)
    if err != nil || !again.NoStartAt.Equal(*receipt.NoStartAt) || !again.CleanedAt.Equal(*receipt.CleanedAt) {
        t.Fatal(again, err)
    }
    state, err := remotestate.Open(req.Root, false)
    if err != nil {
        t.Fatal(err)
    }
    defer state.Close()
    if _, err := state.Run(req.RunID); err == nil {
        t.Fatal("private record survived")
    }
    page, err := state.Events(context.Background(), event.ReadInput{Limit: 10})
    if err != nil || len(page.Events) != 1 || page.Events[0].Type != "run.preparation_abandoned" {
        t.Fatal(page, err)
    }
    req.Args = []string{"/bin/true"}
    for _, action := range []string{"start", "launch"} {
        req.Action = action
        _, err := Handle(req)
        expectCode(t, err, "RUN_ABANDONED")
    }
    if _, err := copyWork(req, dir); err == nil || err.Error() != "RUN_ABANDONED" {
        t.Fatal(err)
    }
}

func TestAbandonAbsentRootFencesDelayedReceiveBeforeCacheMutation(t *testing.T) {
    root := filepath.Join(t.TempDir(), "absent")
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: "never_received"}
    preview, err := Abandon(context.Background(), req, true)
    if err != nil || preview.NoStartAt != nil {
        t.Fatal(preview, err)
    }
    if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("preview created root", err)
    }
    done, err := Abandon(context.Background(), req, false)
    if err != nil || done.NoStartAt == nil || done.CleanedAt == nil || done.PayloadPresent {
        t.Fatal(done, err)
    }
    before := treeContents(t, root)
    err = receive(strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, []string{wire.Version, root, "project", "--run", req.RunID, "--server", "-x", ".", "cache"})
    expectCode(t, err, "RUN_ABANDONED")
    if !reflect.DeepEqual(before, treeContents(t, root)) {
        t.Fatal("late receive changed cache")
    }
    req.Action, req.Args = "start", []string{"/bin/true"}
    _, err = Handle(req)
    expectCode(t, err, "RUN_ABANDONED")
}

func TestAbandonPartialCopyFailure(t *testing.T) {
    root := managedRoot(t)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: "partial"}
    err := updateCache(root, "project", func(cache string, _ *os.File) error {
        writeSource(t, cache, "a-partial", "copied")
        if err := unix.Mkfifo(filepath.Join(cache, "z-unsupported"), 0600); err != nil {
            return err
        }
        dir, err := runPath(req)
        if err != nil {
            return err
        }
        _, err = copyWork(req, dir)
        return err
    })
    expectCode(t, err, "COPY_FAILED")
    dir := filepath.Join(root, "runs", req.RunID)
    if _, err := os.Stat(filepath.Join(dir, "work", "a-partial")); err != nil {
        t.Fatal("no partial copy", err)
    }
    receipt, err := Abandon(context.Background(), req, false)
    if err != nil || receipt.CleanedAt == nil {
        t.Fatal(receipt, err)
    }
    if _, err := os.Stat(filepath.Join(root, "cache", "a-partial")); err != nil {
        t.Fatal("shared cache removed", err)
    }
}

func TestAbandonRefusesLaunchAdmissionAndBusyOwners(t *testing.T) {
    t.Run("unknown prepared", func(t *testing.T) {
        req, dir := abandonFixture(t)
        r, _ := read(dir)
        r.Status = "unknown"
        if err := save(dir, r); err != nil {
            t.Fatal(err)
        }
        before := treeContents(t, req.Root)
        _, err := Abandon(context.Background(), req, false)
        expectCode(t, err, "ABANDON_NOT_PREPARED")
        if !reflect.DeepEqual(before, treeContents(t, req.Root)) {
            t.Fatal("unknown preparation changed")
        }
    })
    for _, phase := range []string{"starting", "setup", "command"} {
        t.Run(phase, func(t *testing.T) {
            req, dir := abandonFixture(t)
            r, _ := read(dir)
            r.Phase = phase
            if err := save(dir, r); err != nil {
                t.Fatal(err)
            }
            before := treeContents(t, req.Root)
            _, err := Abandon(context.Background(), req, false)
            expectCode(t, err, "ABANDON_NOT_PREPARED")
            if !reflect.DeepEqual(before, treeContents(t, req.Root)) {
                t.Fatal("refusal mutated state")
            }
        })
    }
    for _, name := range []string{"project", "data", "launch.lock", "owner.lock"} {
        t.Run(name, func(t *testing.T) {
            req, dir := abandonFixture(t)
            var lock *os.File
            var err error
            switch name {
            case "project":
                lock, err = lockProject(req.Root)
            case "data":
                lock, err = lockData(dir, false)
            default:
                lock, err = cleanupLock(filepath.Join(dir, name), true)
            }
            if err != nil {
                t.Fatal(err)
            }
            defer lock.Close()
            _, err = Abandon(context.Background(), req, false)
            expectCode(t, err, "ABANDON_BUSY")
            r, err := read(dir)
            if err != nil || r.Phase != "prepared" {
                t.Fatal(r, err)
            }
        })
    }
}

type cancelAfterFence struct {
    context.Context
    root, id string
}

func (c cancelAfterFence) Err() error {
    s, err := remotestate.Open(c.root, false)
    if err != nil {
        return nil
    }
    defer s.Close()
    r, _ := s.Abandonment(c.id)
    if r != nil {
        return context.Canceled
    }
    return nil
}

func TestAbandonCancellationRetainsFenceAndRejectsReplacement(t *testing.T) {
    req, dir := abandonFixture(t)
    receipt, err := Abandon(cancelAfterFence{context.Background(), req.Root, req.RunID}, req, false)
    expectCode(t, err, "ABANDON_UNCONFIRMED")
    if receipt == nil || receipt.NoStartAt == nil || receipt.CleanedAt != nil {
        t.Fatal(receipt)
    }
    renamed := dir + "-original"
    if err := os.Rename(dir, renamed); err != nil {
        t.Fatal(err)
    }
    if err := os.Mkdir(dir, 0700); err != nil {
        t.Fatal(err)
    }
    writeSource(t, dir, "sentinel", "keep")
    _, err = Abandon(context.Background(), req, false)
    expectCode(t, err, "ABANDON_UNSAFE")
    if err := os.Remove(filepath.Join(dir, "sentinel")); err != nil {
        t.Fatal(err)
    }
    if err := os.Remove(dir); err != nil {
        t.Fatal(err)
    }
    if err := os.Rename(renamed, dir); err != nil {
        t.Fatal(err)
    }
    done, err := Abandon(context.Background(), req, false)
    if err != nil || done.CleanedAt == nil || !done.NoStartAt.Equal(*receipt.NoStartAt) {
        t.Fatal(done, err)
    }
}

func TestAbandonUnknownFormatsAndQueueClaimsStayUntouched(t *testing.T) {
    req, _ := abandonFixture(t)
    testUnknownRecord(t, req.Root, req.RunID, `{"format_version":99}`)
    before := treeContents(t, req.Root)
    _, err := Abandon(context.Background(), req, false)
    expectCode(t, err, "BAD_FORMAT")
    if !reflect.DeepEqual(before, treeContents(t, req.Root)) {
        t.Fatal("unknown format changed")
    }
    queueRoot, _, _ := queueFixture(t)
    req, _ = abandonFixture(t)
    if err := claimQueuedRun(queueRoot, req.RunID); err != nil {
        t.Fatal(err)
    }
    before = treeContents(t, req.Root)
    _, err = Abandon(context.Background(), req, false)
    expectCode(t, err, "ABANDON_USE_QUEUE")
    if !reflect.DeepEqual(before, treeContents(t, req.Root)) {
        t.Fatal("queue run changed")
    }
}
