package integration

import (
    "context"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

func TestNativeOutputTransfer(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    arch, err := f.client.RunnerArch(ctx, f.target, true)
    if err != nil {
        t.Fatal(err)
    }
    runner, err := (execution.Bootstrap{Remote: f.client}).Verify(ctx, f.target, arch, true)
    if err != nil {
        t.Fatal(err)
    }
    root := `/home/researcher/results 中文 'single' "double" $literal`
    f.shell(`mkdir -p "$1/cache"; printf 'project\n' >"$1/.loaf-project"`, root)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "start", Root: root, ProjectID: "project", RunID: "run", Args: []string{"/bin/sh", "-c", `mkdir -p outputs checkpoints; printf metrics >'outputs/中文 space.json'; printf weights >checkpoints/model.bin; ln -s '中文 space.json' outputs/latest; echo ready >ready; while [ ! -f release ]; do sleep .02; done; exit 7`}}
    if _, err := f.client.Control(ctx, f.target, runner.Path, req, true); err != nil {
        t.Fatal(err)
    }
    waitRunFile(t, f, execution.View{ID: req.RunID, RemoteDir: root}, "work/ready")
    req.Outputs = []string{"outputs", "checkpoints", "missing"}
    selected, err := f.client.Outputs(ctx, f.target, runner.Path, req, true)
    if err != nil || len(selected.Available) != 2 || len(selected.Missing) != 1 {
        t.Fatal(selected, err)
    }
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Fatal(err)
    }
    rsync := ssh.Rsync{Client: f.client, Binary: binary}
    destination := t.TempDir()
    for _, want := range []int{3, 0} {
        result, err := rsync.Pull(ctx, f.target, runner.Path, destination, ssh.PullInput{Root: root, ProjectID: req.ProjectID, RunID: req.RunID, Paths: selected.Available}, true)
        if err != nil || result.ChangedFiles != want {
            var failure *fault.Error
            errors.As(err, &failure)
            t.Fatal(result, err, failure)
        }
    }
    if data, err := os.ReadFile(filepath.Join(destination, "checkpoints/model.bin")); err != nil || string(data) != "weights" {
        t.Fatal(string(data), err)
    }
    if target, err := os.Readlink(filepath.Join(destination, "outputs/latest")); err != nil || target != "中文 space.json" {
        t.Fatal(target, err)
    }
    // No project lock is held by output reads; source sync and stop can proceed.
    f.shell(`ln -s /etc/passwd "$1/runs/run/work/outputs/escape"`, root)
    _, err = f.client.Outputs(ctx, f.target, runner.Path, req, true)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "UNSAFE_PATH" {
        t.Fatal("accepted escaping output", err)
    }
    f.shell(`rm "$1/runs/run/work/outputs/escape"; touch "$1/runs/run/work/release"`, root)
    req.Action = "status"
    wait(t, 5*time.Second, func() bool {
        record, err := f.client.Control(ctx, f.target, runner.Path, req, true)
        return err == nil && record.Status == "failed" && record.ExitCode != nil && *record.ExitCode == 7
    })
}
