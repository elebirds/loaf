package integration

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

func TestAbandonLinuxFilesystem(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" || os.Getenv("LOAF_REMOTE_TEST") == "" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    command(t, "docker", "cp", os.Getenv("LOAF_REMOTE_TEST"), f.id+":/tmp/remote-abandon.test")
    command(t, "docker", "exec", "--user", "researcher", f.id, "/tmp/remote-abandon.test", "-test.v", "-test.run", "^TestAbandon")
}

func TestPackagedAbandonFailedPreparationAndRetry(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    sourceFile(t, project, "input.txt", "source")
    sourceFile(t, project, "loaf.toml", "[sync]\nmode='update'\n")
    code, out := cli(t, project, "sync", "--json")
    var synced struct {
        Data struct {
            RemoteDir string `json:"remote_dir"`
        } `json:"data"`
    }
    if code != 0 || json.Unmarshal(out, &synced) != nil || synced.Data.RemoteDir == "" {
        t.Fatal(code, string(out))
    }
    root := synced.Data.RemoteDir
    // A real unsupported cache entry makes os.CopyFS fail after copying files.
    // update mode deliberately retains it; abandonment must not roll it back.
    f.shell(`mkfifo "$1/cache/zz-unsupported"; printf keep >"$1/sentinel"`, root)
    code, out = cli(t, project, "run", "--json", "--", "/bin/sh", "-c", "touch must-not-run")
    var failed struct {
        Data execution.View `json:"data"`
    }
    if code != 1 || json.Unmarshal(out, &failed) != nil || failed.Data.ID == "" || failed.Data.Submission != "preparing" {
        t.Fatal(code, string(out))
    }
    id := failed.Data.ID
    f.shell(`test -f "$1/runs/$2/work/input.txt"; test ! -e "$1/runs/$2/work/must-not-run"`, root, id)
    code, out = cli(t, project, "abandon", id, "--dry-run", "--json")
    var batch struct {
        Data client.AbandonBatch `json:"data"`
    }
    if code != 0 || json.Unmarshal(out, &batch) != nil || len(batch.Data.Runs) != 1 || batch.Data.Runs[0].NoStartAt != nil {
        t.Fatal(code, string(out))
    }
    // The unprivileged fixture yields a real partial-deletion failure.
    f.shell(`mkdir "$1/runs/$2/work/blocked"; printf retained >"$1/runs/$2/work/blocked/item"; chmod 500 "$1/runs/$2/work/blocked"`, root, id)
    code, out = cli(t, project, "abandon", id, "--yes", "--json")
    if code != 3 || json.Unmarshal(out, &batch) != nil || batch.Data.Runs[0].NoStartAt == nil || batch.Data.Runs[0].CleanedAt != nil || batch.Data.Runs[0].Run.Submission != "preparing" {
        t.Fatal(code, string(out))
    }
    proof := *batch.Data.Runs[0].NoStartAt
    code, out = cli(t, project, "inspect", id, "--json")
    var inspected struct {
        Data execution.LocalDetails `json:"data"`
    }
    if code != 0 || json.Unmarshal(out, &inspected) != nil || inspected.Data.Abandonment == nil || inspected.Data.Abandonment.NoStartAt == nil || inspected.Data.Status != "unknown" {
        t.Fatal(code, string(out))
    }
    f.shell(`chmod 700 "$1/runs/$2/work/blocked"`, root, id)
    for i := 0; i < 2; i++ {
        code, out = cli(t, project, "abandon", id, "--yes", "--json")
        if code != 0 || json.Unmarshal(out, &batch) != nil || batch.Data.Runs[0].CleanedAt == nil || !batch.Data.Runs[0].NoStartAt.Equal(proof) {
            t.Fatal(code, string(out))
        }
    }
    f.shell(`test ! -e "$1/runs/$2"; test -p "$1/cache/zz-unsupported"; test "$(cat "$1/sentinel")" = keep`, root, id)
    if got := f.remoteSQL(root, "SELECT count(*) FROM runs WHERE run_id='"+id+"'"); got != "0" {
        t.Fatal("private preparation record remains", got)
    }
    if got := f.remoteSQL(root, "SELECT count(*) FROM event_log WHERE json_extract(payload,'$.type')='run.preparation_abandoned'"); got != "1" {
        t.Fatal("duplicate/missing abandonment event", got)
    }
    code, out = cli(t, project, "clean", id, "--yes", "--json")
    if code != 1 || !strings.Contains(string(out), "CLEAN_NOT_CONFIRMED") {
        t.Fatal("ordinary clean changed contract", code, string(out))
    }
    f.shell(`rm "$1/cache/zz-unsupported"`, root)
    accepted := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/true")
    if accepted.Status != "succeeded" || accepted.Submission != "submitted" {
        t.Fatal("direct submit acquired abandonment prerequisite", accepted)
    }
}

func TestAbandonSSHConfirmationLossAndLaunchRace(t *testing.T) {
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
    info, err := (execution.Bootstrap{Remote: f.client}).Verify(ctx, f.target, arch, true)
    if err != nil {
        t.Fatal(err)
    }
    project := t.TempDir()
    sourceFile(t, project, "input.txt", "prepared")
    root := "/home/researcher/abandon SSH 中文"
    sender := ssh.Rsync{Client: f.client, Binary: "rsync"}
    prepare := func(id string) wire.Request {
        t.Helper()
        if _, err := sender.Sync(ctx, f.target, info.Path, project, ssh.SyncInput{Root: root, ProjectID: "project", RunID: id, Mode: "mirror", Paths: []string{"input.txt"}}, true); err != nil {
            t.Fatal(err)
        }
        return wire.Request{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: id}
    }
    request := prepare("lost")
    wrapper := "/home/researcher/abandon-lost-runner"
    script := "#!/bin/sh\nprintf call >>" + ssh.Quote(wrapper+".count") + "\n" + ssh.Quote(info.Path) + " \"$@\" >/dev/null\nexit 255\n"
    f.shell(`printf '%s' "$1" >"$2"; chmod 700 "$2"`, script, wrapper)
    if receipt, err := f.client.Abandon(ctx, f.target, wrapper, request, false, true); err == nil || receipt != nil {
        t.Fatal(receipt, err)
    }
    f.shell(`test "$(cat "$1.count")" = call`, wrapper)
    receipt, err := f.client.Abandon(ctx, f.target, info.Path, request, false, true)
    if err != nil || receipt.NoStartAt == nil || receipt.CleanedAt == nil {
        t.Fatal(receipt, err)
    }
    request = prepare("racing")
    launch := request
    launch.Action, launch.Args = "launch", []string{"/bin/sh", "-c", "printf started >" + ssh.Quote(filepath.ToSlash(root+"/race-started"))}
    var wg sync.WaitGroup
    var launched *wire.RunRecord
    var launchErr, abandonErr error
    var abandoned *wire.AbandonReceipt
    start := make(chan struct{})
    wg.Add(2)
    go func() {
        defer wg.Done()
        <-start
        launched, launchErr = f.client.Control(ctx, f.target, info.Path, launch, true)
    }()
    go func() {
        defer wg.Done()
        <-start
        abandoned, abandonErr = f.client.Abandon(ctx, f.target, info.Path, request, false, true)
    }()
    close(start)
    wg.Wait()
    if abandoned != nil && abandoned.NoStartAt != nil {
        if launchErr == nil || launched != nil && launched.SupervisorPID != 0 {
            t.Fatal("both launch and abandonment won", launched, launchErr, abandoned, abandonErr)
        }
        f.shell(`test ! -e "$1/race-started"`, root)
    } else {
        if abandonErr == nil {
            t.Fatal("abandonment lacked proof", abandoned)
        }
        var remote *ssh.RemoteError
        if !errors.As(abandonErr, &remote) || remote.Code != "ABANDON_BUSY" && remote.Code != "ABANDON_NOT_PREPARED" {
            t.Fatal(abandonErr)
        }
        if launchErr != nil {
            t.Fatal(launchErr)
        }
        wait(t, 5*time.Second, func() bool {
            return exec.Command("docker", "exec", f.id, "test", "-s", root+"/race-started").Run() == nil
        })
        f.shell(`test "$(cat "$1/race-started")" = started`, root)
    }
}
