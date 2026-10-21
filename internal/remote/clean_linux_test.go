package remote

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

func cleanFixture(t *testing.T) (wire.Request, string) {
    t.Helper()
    req, work := outputFixture(t)
    record, err := read(filepath.Dir(work))
    if err != nil {
        t.Fatal(err)
    }
    record.Status, record.Phase = "failed", "command"
    record.CreatedAt = time.Now().UTC().Add(-time.Minute)
    completed := time.Now().UTC()
    record.CompletedAt, record.ExitCode = &completed, new(7)
    if err := save(filepath.Dir(work), record); err != nil {
        t.Fatal(err)
    }
    return req, filepath.Dir(work)
}

func treeContents(t *testing.T, root string) map[string]string {
    t.Helper()
    result := map[string]string{}
    err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
        if err != nil {
            return err
        }
        rel, _ := filepath.Rel(root, name)
        if entry.Type().IsRegular() {
            data, err := os.ReadFile(name)
            if err != nil {
                return err
            }
            result[rel] = string(data)
        } else if entry.Type()&os.ModeSymlink != 0 {
            result[rel], err = os.Readlink(name)
            return err
        } else {
            result[rel] = entry.Type().String()
        }
        return nil
    })
    if err != nil {
        t.Fatal(err)
    }
    return result
}

func TestCleanPreviewIsReadOnlyAndRemovalRetainsIDReceipt(t *testing.T) {
    req, dir := cleanFixture(t)
    writeSource(t, req.Root, "cache/source", "cache")
    writeSource(t, req.Root, "runs/other/work/sentinel", "other")
    outside := t.TempDir()
    writeSource(t, outside, "sentinel", "outside")
    if err := os.Symlink(outside, filepath.Join(dir, "work/escape")); err != nil {
        t.Fatal(err)
    }
    before := treeContents(t, req.Root)
    preview, err := Clean(context.Background(), req, true)
    if err != nil || preview == nil || preview.CleanedAt != nil || preview.Summary.Status != "failed" {
        t.Fatal(preview, err)
    }
    if after := treeContents(t, req.Root); !reflect.DeepEqual(before, after) {
        t.Fatal("preview changed files", before, after)
    }
    receipt, err := Clean(context.Background(), req, false)
    if err != nil || receipt.CleanedAt == nil || *receipt.Summary.ExitCode != 7 {
        t.Fatal(receipt, err)
    }
    for _, name := range []string{"work", "output.log"} {
        if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
            t.Fatal("retained payload", name, err)
        }
    }
    for _, name := range []string{filepath.Join(req.Root, "cache/source"), filepath.Join(req.Root, "runs/other/work/sentinel"), filepath.Join(outside, "sentinel")} {
        if _, err := os.Stat(name); err != nil {
            t.Fatal("deleted outside scope", name, err)
        }
    }
    if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("retained cleaned run directory", err)
    }
    bytes, _ := json.Marshal(receipt)
    if strings.Contains(string(bytes), "SECRET") || strings.Contains(string(bytes), "must-not-leak") {
        t.Fatal(string(bytes))
    }
    again, err := Clean(context.Background(), req, false)
    if err != nil || !again.CleanedAt.Equal(*receipt.CleanedAt) {
        t.Fatal(again, err)
    }
    req.Args = []string{"/bin/sh", "-c", "touch should-not-run"}
    for _, action := range []string{"start", "launch"} {
        req.Action = action
        _, err := Handle(req)
        expectCode(t, err, "RUN_CLEANED")
    }
    _, err = Outputs(context.Background(), req)
    expectCode(t, err, "RUN_CLEANED")
}

func TestCleanRefusesUnknownFormatsActiveAndHeldLifetimes(t *testing.T) {
    for _, outcome := range []string{"running", "starting", "unknown"} {
        t.Run(outcome, func(t *testing.T) {
            req, dir := cleanFixture(t)
            r, _ := read(dir)
            r.Status = outcome
            save(dir, r)
            _, err := Clean(context.Background(), req, false)
            expectCode(t, err, "CLEAN_NOT_TERMINAL")
            if _, err := os.Stat(filepath.Join(dir, "work")); err != nil {
                t.Fatal(err)
            }
        })
    }
    req, dir := cleanFixture(t)
    owner, err := cleanupLock(filepath.Join(dir, "owner.lock"), true)
    if err != nil {
        t.Fatal(err)
    }
    _, err = Clean(context.Background(), req, false)
    expectCode(t, err, "CLEAN_BUSY")
    owner.Close()
    mismatch, _ := json.Marshal(wire.RunRecord{FormatVersion: 1, Request: wire.Request{FormatVersion: 1, Version: wire.Version, ProjectID: "other"}})
    for _, text := range []string{`{"format_version":99}`, string(mismatch)} {
        req, dir := cleanFixture(t)
        os.WriteFile(filepath.Join(dir, "run.json"), []byte(text), 0600)
        before := treeContents(t, req.Root)
        _, err := Clean(context.Background(), req, false)
        expectCode(t, err, "BAD_FORMAT")
        data, _ := os.ReadFile(filepath.Join(dir, "run.json"))
        if string(data) != text || !reflect.DeepEqual(before, treeContents(t, req.Root)) {
            t.Fatal("changed unknown record or created metadata", string(data))
        }
    }
}

func TestCleanRefusesConcurrentDataTransferAndUnsafeRunLinks(t *testing.T) {
    req, dir := cleanFixture(t)
    transfer, err := lockData(dir, false)
    if err != nil {
        t.Fatal(err)
    }
    _, err = Clean(context.Background(), req, false)
    expectCode(t, err, "CLEAN_BUSY")
    if _, err := Outputs(context.Background(), req); err != nil {
        t.Fatal("shared transfer lock should allow inspection", err)
    }
    transfer.Close()
    moved := dir + "-outside"
    if err := os.Rename(dir, moved); err != nil {
        t.Fatal(err)
    }
    if err := os.Symlink(moved, dir); err != nil {
        t.Fatal(err)
    }
    _, err = Clean(context.Background(), req, false)
    expectCode(t, err, "UNSAFE_PATH")
    if _, err := os.Stat(filepath.Join(moved, "work")); err != nil {
        t.Fatal(err)
    }
}

type cancelOnCheck struct {
    context.Context
    cancel context.CancelFunc
    checks int
}

func (c *cancelOnCheck) Err() error {
    c.checks++
    if c.checks == 12 {
        c.cancel()
    }
    return c.Context.Err()
}

func TestCleanCancellationLeavesRetryableReceiptAndProtectedID(t *testing.T) {
    req, dir := cleanFixture(t)
    for n := 0; n < 60; n++ {
        writeSource(t, dir, filepath.Join("work/bulk", time.Unix(int64(n), 0).Format("150405")), "payload")
    }
    parent, cancel := context.WithCancel(context.Background())
    defer cancel()
    ctx := &cancelOnCheck{Context: parent, cancel: cancel}
    receipt, err := Clean(ctx, req, false)
    expectCode(t, err, "CLEAN_UNCONFIRMED")
    if receipt == nil || receipt.CleanedAt != nil {
        t.Fatal(receipt)
    }
    saved, err := cleanupReceipt(req, dir)
    if err != nil || saved == nil || saved.CleanedAt != nil {
        t.Fatal(saved, err)
    }
    req.Args, req.Action = []string{"/bin/true"}, "start"
    _, err = Handle(req)
    expectCode(t, err, "CLEAN_UNCONFIRMED")
    complete, err := Clean(context.Background(), req, false)
    if err != nil || complete.CleanedAt == nil {
        t.Fatal(complete, err)
    }
    if _, err := os.Stat(filepath.Join(dir, "work")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal(err)
    }
}

func TestCleanReceiptFormatAndReplacedDirectoryStayUntouched(t *testing.T) {
    req, dir := cleanFixture(t)
    original := []byte(`{"format_version":99}`)
    if err := os.WriteFile(cleanupFile(dir), original, 0600); err != nil {
        t.Fatal(err)
    }
    _, err := Clean(context.Background(), req, false)
    expectCode(t, err, "BAD_FORMAT")
    after, _ := os.ReadFile(cleanupFile(dir))
    if !bytes.Equal(original, after) {
        t.Fatal(string(after))
    }
    os.Remove(cleanupFile(dir))
    receipt, err := Clean(context.Background(), req, true)
    if err != nil {
        t.Fatal(err)
    }
    receipt.Inode++
    if err := saveCleanup(dir, receipt); err != nil {
        t.Fatal(err)
    }
    _, err = Clean(context.Background(), req, false)
    expectCode(t, err, "CLEAN_UNSAFE")
    if _, err := os.Stat(filepath.Join(dir, "work")); err != nil {
        t.Fatal(err)
    }
    if err := unix.Unlink(cleanupFile(dir)); err != nil {
        t.Fatal(err)
    }
}

func TestCurrentVersionGateRejectsMismatchedRecordsWithoutMutation(t *testing.T) {
    req, dir := cleanFixture(t)
    record, err := read(dir)
    if err != nil {
        t.Fatal(err)
    }
    record.Request.Version = "different-version"
    if err := save(dir, record); err != nil {
        t.Fatal(err)
    }
    before := treeContents(t, req.Root)
    for _, action := range []string{"status", "stop", "launch", "start"} {
        req.Action = action
        req.Args = []string{"/bin/true"}
        _, err := Handle(req)
        expectCode(t, err, "VERSION_MISMATCH")
        if !reflect.DeepEqual(before, treeContents(t, req.Root)) {
            t.Fatal("mutated mismatched task", action)
        }
    }
    _, err = Clean(context.Background(), req, false)
    expectCode(t, err, "VERSION_MISMATCH")
    if !reflect.DeepEqual(before, treeContents(t, req.Root)) {
        t.Fatal("cleanup mutated mismatched task")
    }
}
