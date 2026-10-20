package ssh

import (
    "context"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func nativePullFixture(t *testing.T) (Rsync, string, string, string) {
    t.Helper()
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Skip("system rsync is unavailable")
    }
    dir := t.TempDir()
    remote := filepath.Join(dir, `remote 中文 'single' "double" $literal`)
    sender := filepath.Join(dir, `sender 'single' "double"`)
    script := "#!/bin/sh\nset -eu\ncd \"$3/runs/$5/work\"\nshift 6\nexec " + Quote(binary) + " \"$@\"\n"
    if err := os.WriteFile(sender, []byte(script), 0700); err != nil {
        t.Fatal(err)
    }
    fakeSSH := filepath.Join(dir, `ssh 'single' "double"`)
    script = "#!/bin/sh\nset -eu\nwhile [ \"$1\" != -- ]; do shift; done\nshift 2\nexec /bin/sh -c \"$*\"\n"
    if err := os.WriteFile(fakeSSH, []byte(script), 0700); err != nil {
        t.Fatal(err)
    }
    return Rsync{Client: &Client{Binary: fakeSSH}, Binary: binary}, remote, sender, t.TempDir()
}

func TestNativeOutputDownloadIsLiteralIncrementalAndNeverDeletesExtras(t *testing.T) {
    r, remote, sender, destination := nativePullFixture(t)
    names := []string{"outputs/中文 space.txt", "outputs/back\\slash[1]*?", "outputs/line\nbreak", "checkpoints/model.bin", "empty"}
    work := filepath.Join(remote, "runs", "run", "work")
    for _, name := range names[:len(names)-1] {
        file := filepath.Join(work, name)
        if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(file, []byte("initial"), 0600); err != nil {
            t.Fatal(err)
        }
    }
    if err := os.MkdirAll(filepath.Join(work, "empty"), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.Symlink("中文 space.txt", filepath.Join(work, "outputs/latest")); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(destination, "keep-local.txt"), []byte("keep"), 0600); err != nil {
        t.Fatal(err)
    }
    input := PullInput{Root: remote, ProjectID: "project", RunID: "run", Paths: []string{"outputs", "checkpoints", "empty"}}
    pull := func(want int) {
        t.Helper()
        result, err := r.Pull(context.Background(), config.Target{Destination: "fixture"}, sender, destination, input, true)
        if err != nil || result.ChangedFiles != want {
            var f *fault.Error
            errors.As(err, &f)
            t.Fatal(result, err, f)
        }
    }
    pull(5)
    pull(0)
    source := filepath.Join(work, "checkpoints/model.bin")
    info, err := os.Stat(source)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(source, []byte("changed"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
        t.Fatal(err)
    }
    pull(0) // Native quick-check; same size and timestamp.
    input.Checksum = true
    pull(1)
    if data, err := os.ReadFile(filepath.Join(destination, "checkpoints/model.bin")); err != nil || string(data) != "changed" {
        t.Fatal(string(data), err)
    }
    // Explicitly changing mtime makes a normal quick-check detect the update.
    input.Checksum = false
    later := info.ModTime().Add(2 * time.Second)
    if err := os.Chtimes(source, later, later); err != nil {
        t.Fatal(err)
    }
    pull(1)
    for _, name := range names {
        if _, err := os.Stat(filepath.Join(destination, name)); err != nil {
            t.Fatal(name, err)
        }
    }
    if target, err := os.Readlink(filepath.Join(destination, "outputs/latest")); err != nil || target != "中文 space.txt" {
        t.Fatal(target, err)
    }
    if err := os.Remove(source); err != nil {
        t.Fatal(err)
    }
    pull(0)
    if _, err := os.Stat(filepath.Join(destination, "checkpoints/model.bin")); err != nil {
        t.Fatal("deleted local extra", err)
    }
    if _, err := os.Stat(filepath.Join(destination, "keep-local.txt")); err != nil {
        t.Fatal("deleted unrelated local file", err)
    }
}

func TestDownloadDiagnosticsAndBoundedItemizedOutput(t *testing.T) {
    var count changeCounter
    for _, text := range []string{"LOAF_", "CHANGE:>f+++++++++\n", "LOAF_CHANGE:cd+++++++++\nLOAF_CHANGE:cL+++++++++\n", strings.Repeat("noise", 1000) + "\n"} {
        _, _ = count.Write([]byte(text))
    }
    if count.count != 2 || count.pending != "" {
        t.Fatal(count)
    }
    var tail diagnosticTail
    _, _ = tail.Write([]byte(strings.Repeat("x", 100000)))
    _, _ = tail.Write([]byte("\nLOAF_PULL_ERROR:UNSAFE_PATH\n"))
    match := pullCode.FindStringSubmatch(tail.String())
    if len(tail.data) != 64*1024 || len(match) != 2 || match[1] != "UNSAFE_PATH" {
        t.Fatal(len(tail.data))
    }
    for _, code := range []string{"UNSAFE_PATH", "OUTPUT_MISSING", "BAD_FORMAT", "PULL_CANCELED"} {
        f := pullFailure(code, errors.New("native exit"), "original diagnostic")
        if f.Code != code || f.Params["Detail"] != "original diagnostic" || f.Hint == "" {
            t.Fatal(f)
        }
    }
}
