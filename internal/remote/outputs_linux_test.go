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

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

func outputFixture(t *testing.T) (wire.Request, string) {
    t.Helper()
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Root: managedRoot(t), ProjectID: "project", RunID: "run", Outputs: []string{"outputs", "checkpoints"}}
    dir := filepath.Join(req.Root, "runs", req.RunID)
    work := filepath.Join(dir, "work")
    writeSource(t, work, "outputs/中文 space.json", "metrics")
    writeSource(t, work, "checkpoints/model.bin", "weights")
    record := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "unknown"}
    record.Request.Env = map[string]string{"SECRET": "must-not-leak"}
    if err := seedRecord(dir, record); err != nil {
        t.Fatal(err)
    }
    return req, work
}

func TestOutputsAreReadOnlyAndDoNotNeedLiveSupervisorOrProjectLock(t *testing.T) {
    req, work := outputFixture(t)
    req.Outputs = append(req.Outputs, "not-written")
    if err := os.Symlink("中文 space.json", filepath.Join(work, "outputs/latest")); err != nil {
        t.Fatal(err)
    }
    lock, err := lockProject(req.Root)
    if err != nil {
        t.Fatal(err)
    }
    defer lock.Close()
    before, err := recordBytes(filepath.Dir(work))
    if err != nil {
        t.Fatal(err)
    }
    selection, err := Outputs(context.Background(), req)
    if err != nil || !reflect.DeepEqual(selection.Available, []string{"outputs", "checkpoints"}) || !reflect.DeepEqual(selection.Missing, []string{"not-written"}) {
        t.Fatal(selection, err)
    }
    req.Action = "outputs"
    input, _ := json.Marshal(req)
    var out bytes.Buffer
    if code := Serve(bytes.NewReader(input), &out); code != 0 || strings.Contains(out.String(), "must-not-leak") || strings.Contains(out.String(), "record") {
        t.Fatal(code, out.String())
    }
    after, err := recordBytes(filepath.Dir(work))
    if err != nil || !bytes.Equal(before, after) {
        t.Fatal("read rewrote run record", err)
    }
    if err := os.RemoveAll(filepath.Join(req.Root, "runs")); err != nil {
        t.Fatal(err)
    }
    _, err = Outputs(context.Background(), req)
    expectCode(t, err, "RUN_NOT_FOUND")
    if _, err := os.Stat(filepath.Join(req.Root, "runs")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("read created missing runs directory", err)
    }
}

func TestOutputsRejectUnsafeSelectionsAndLinksBeforeTransfer(t *testing.T) {
    for _, name := range []string{".", "../escape", "/absolute", "outputs/../checkpoints", "bad\nname"} {
        t.Run(name, func(t *testing.T) {
            req, _ := outputFixture(t)
            req.Outputs = []string{name}
            _, err := Outputs(context.Background(), req)
            expectCode(t, err, "UNSAFE_PATH")
        })
    }
    for _, target := range []string{"/etc/passwd", "../../outside", "missing", "../checkpoints/model.bin"} {
        t.Run(target, func(t *testing.T) {
            req, work := outputFixture(t)
            if err := os.Symlink(target, filepath.Join(work, "outputs/link")); err != nil {
                t.Fatal(err)
            }
            req.Outputs = []string{"outputs"}
            _, err := Outputs(context.Background(), req)
            expectCode(t, err, "UNSAFE_PATH")
        })
    }
    req, work := outputFixture(t)
    if err := unix.Mkfifo(filepath.Join(work, "outputs/pipe"), 0600); err != nil {
        t.Fatal(err)
    }
    _, err := Outputs(context.Background(), req)
    expectCode(t, err, "UNSAFE_PATH")
    if err := os.Remove(filepath.Join(work, "outputs/pipe")); err != nil {
        t.Fatal(err)
    }
    if err := os.Symlink("outputs", filepath.Join(work, "linked")); err != nil {
        t.Fatal(err)
    }
    req.Outputs = []string{"linked/中文 space.json"}
    _, err = Outputs(context.Background(), req)
    expectCode(t, err, "UNSAFE_PATH")
    req.Outputs = []string{"linked"}
    _, err = Outputs(context.Background(), req)
    expectCode(t, err, "UNSAFE_PATH")
}

func TestOutputFormatsAndIdentityAreNotRewritten(t *testing.T) {
    req, work := outputFixture(t)
    mismatch, _ := json.Marshal(wire.RunRecord{FormatVersion: 1, Request: wire.Request{FormatVersion: 1, Version: wire.Version, ProjectID: "other"}})
    for _, text := range []string{`{"format_version":99}`, string(mismatch)} {
        testUnknownRecord(t, req.Root, req.RunID, text)
        before, _ := recordBytes(filepath.Dir(work))
        _, err := Outputs(context.Background(), req)
        expectCode(t, err, "BAD_FORMAT")
        data, _ := recordBytes(filepath.Dir(work))
        if !bytes.Equal(data, before) {
            t.Fatal("rewrote incompatible record")
        }
    }
}

func TestOutputSenderPassesNativeStreamAndOptionsWithoutFraming(t *testing.T) {
    req, _ := outputFixture(t)
    paths, _ := json.Marshal(req.Outputs)
    var out, diagnostic bytes.Buffer
    bad := []string{wire.Version, req.Root, req.ProjectID, req.RunID, string(paths), "--server", "-rlt", ".", "."}
    if SendOutputs(strings.NewReader("opaque"), &out, &diagnostic, bad) != 1 || out.Len() != 0 || !strings.Contains(diagnostic.String(), "BAD_SENDER") {
        t.Fatal(out.String(), diagnostic.String())
    }
    bin := t.TempDir()
    // An unfamiliar native option is passed through; the wrapper does not keep
    // a version-specific whitelist or consume a private prefix from the stream.
    if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte("#!/bin/sh\ncat\n"), 0700); err != nil {
        t.Fatal(err)
    }
    t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
    args := []string{wire.Version, req.Root, req.ProjectID, req.RunID, string(paths), "--server", "--sender", "--native-future-option", ".", "."}
    out.Reset()
    diagnostic.Reset()
    if SendOutputs(strings.NewReader("native protocol unchanged"), &out, &diagnostic, args) != 0 || out.String() != "native protocol unchanged" {
        t.Fatal(out.String(), diagnostic.String())
    }
}

func TestOutputsRejectMismatchedRecordVersionWithoutMutation(t *testing.T) {
    req, work := outputFixture(t)
    record, err := read(filepath.Dir(work))
    if err != nil {
        t.Fatal(err)
    }
    record.Request.Version = "mismatched-version"
    if err := seedRecord(filepath.Dir(work), record); err != nil {
        t.Fatal(err)
    }
    before := treeContents(t, req.Root)
    _, err = Outputs(context.Background(), req)
    expectCode(t, err, "VERSION_MISMATCH")
    if !reflect.DeepEqual(before, treeContents(t, req.Root)) {
        t.Fatal("changed incompatible run")
    }
}
