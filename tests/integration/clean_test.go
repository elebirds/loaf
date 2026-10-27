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

func TestCleanLinuxFilesystem(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" || os.Getenv("LOAF_REMOTE_TEST") == "" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    command(t, "docker", "cp", os.Getenv("LOAF_REMOTE_TEST"), f.id+":/tmp/remote-clean.test")
    command(t, "docker", "exec", "--user", "researcher", f.id, "/tmp/remote-clean.test", "-test.v", "-test.run", "^(TestClean|TestCurrentVersionGate|TestOutputsRejectMismatchedRecordVersion)")
}

func TestCleanRemoteRetryAndTransferLock(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
    defer cancel()
    arch, err := f.client.RunnerArch(ctx, f.target, true)
    if err != nil {
        t.Fatal(err)
    }
    helper, err := (execution.Bootstrap{Remote: f.client}).Verify(ctx, f.target, arch, true)
    if err != nil {
        t.Fatal(err)
    }
    root := `/home/researcher/clean 中文 'quoted' $literal`
    f.managedRemote(root)
    f.shell(`printf shared >"$1/shared"`, root)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "start", Root: root, ProjectID: "project", RunID: "run", Args: []string{"/bin/sh", "-c", "mkdir outputs; printf weights >outputs/model.bin; exit 7"}}
    if _, err := f.client.Control(ctx, f.target, helper.Path, req, true); err != nil {
        t.Fatal(err)
    }
    req.Action = "status"
    wait(t, 5*time.Second, func() bool {
        record, err := f.client.Control(ctx, f.target, helper.Path, req, true)
        return err == nil && record.Status == "failed" && record.ExitCode != nil && *record.ExitCode == 7
    })
    // Stall the actual native remote rsync at an observable readiness point.
    // The current sender already owns its shared data lock at this point.
    bin := "/home/researcher/clean-transfer-bin"
    wrapper := "/home/researcher/clean-transfer-runner"
    script := "#!/bin/sh\nprintf ready >" + ssh.Quote(bin+"/ready") + "\nwhile [ ! -f " + ssh.Quote(bin+"/release") + " ]; do sleep .02; done\nexec /usr/bin/rsync \"$@\"\n"
    f.shell(`mkdir -p "$1"; printf '%s' "$2" >"$1/rsync"; chmod 700 "$1/rsync"; printf '%s' "$3" >"$4"; chmod 700 "$4"`, bin, script, "#!/bin/sh\nPATH="+ssh.Quote(bin)+":$PATH; export PATH\nexec "+ssh.Quote(helper.Path)+" \"$@\"\n", wrapper)
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Fatal(err)
    }
    destination := t.TempDir()
    transfer := make(chan error, 1)
    go func() {
        _, err := (ssh.Rsync{Client: f.client, Binary: binary}).Pull(ctx, f.target, wrapper, destination, ssh.PullInput{Root: root, ProjectID: "project", RunID: "run", Paths: []string{"outputs"}}, true)
        transfer <- err
    }()
    wait(t, 5*time.Second, func() bool {
        return exec.Command("docker", "exec", f.id, "test", "-s", bin+"/ready").Run() == nil
    })
    _, err = f.client.Clean(ctx, f.target, helper.Path, req, true)
    var remote *ssh.RemoteError
    if !errors.As(err, &remote) || remote.Code != "CLEAN_BUSY" {
        t.Fatal("cleanup did not refuse active transfer", err)
    }
    req.Action = "stop"
    stopped, err := f.client.Control(ctx, f.target, helper.Path, req, true)
    if err != nil || stopped.Status != "failed" || *stopped.ExitCode != 7 {
        t.Fatal("transfer unexpectedly held execution control", stopped, err)
    }
    f.shell(`touch "$1/release"`, bin)
    if err := <-transfer; err != nil {
        t.Fatal(err)
    }
    if data, err := os.ReadFile(filepath.Join(destination, "outputs/model.bin")); err != nil || string(data) != "weights" {
        t.Fatal(string(data), err)
    }
    // A real permission failure leaves partial deletion unconfirmed. It must
    // remain retryable without changing the terminal result or local download.
    f.shell(`mkdir "$1/runs/run/blocked"; printf keep >"$1/runs/run/blocked/item"; chmod 500 "$1/runs/run/blocked"`, root)
    receipt, err := f.client.Clean(ctx, f.target, helper.Path, req, true)
    if !errors.As(err, &remote) || remote.Code != "CLEAN_UNCONFIRMED" || receipt == nil || receipt.CleanedAt != nil {
        t.Fatal(receipt, err)
    }
    f.shell(`chmod 700 "$1/runs/run/blocked"`, root)
    receipt, err = f.client.Clean(ctx, f.target, helper.Path, req, true)
    if err != nil || receipt.CleanedAt == nil || receipt.Summary.Status != "failed" || *receipt.Summary.ExitCode != 7 {
        t.Fatal(receipt, err)
    }
    f.shell(`test ! -e "$1/runs/run"; test ! -e "$1/runs/run/work"; test -f "$1/shared"; test -d "$1/cache"`, root)
    if data, err := os.ReadFile(filepath.Join(destination, "outputs/model.bin")); err != nil || string(data) != "weights" {
        t.Fatal("cleanup changed local result", string(data), err)
    }
    // A later pull is a structured unavailable-data result, not empty success.
    req.Outputs = []string{"outputs"}
    _, err = f.client.Outputs(ctx, f.target, helper.Path, req, true)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "RUN_CLEANED" {
        t.Fatal(err)
    }
}
