package integration

import (
    "context"
    "encoding/json"
    "os"
    "strings"
    "sync"
    "testing"
    "time"

    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

func runCLI(t *testing.T, project string, args ...string) execution.View {
    t.Helper()
    code, out := cli(t, project, args...)
    if code != 0 {
        t.Fatalf("%v: %d %s", args, code, out)
    }
    var response struct {
        Data execution.View `json:"data"`
    }
    if err := json.Unmarshal(out, &response); err != nil || response.Data.ID == "" {
        t.Fatal(string(out), err)
    }
    return response.Data
}

func TestDurableRunAndBoundStatus(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := t.TempDir()
    sourceFile(t, project, "train.sh", "echo once >> count\necho original > ready\nwhile [ ! -f release ]; do sleep .05; done\nexit 7\n")
    for _, args := range [][]string{{"add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"use", "lab", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    submitted := runCLI(t, project, "run", "--json", "--", "/bin/sh", "train.sh")
    wait(t, 5*time.Second, func() bool {
        return f.shell("if test -f \"$1/runs/$2/work/ready\"; then cat \"$1/runs/$2/work/ready\"; fi", submitted.RemoteDir, submitted.ID) == "original"
    })
    sourceFile(t, project, "train.sh", "echo changed\n")
    if code, out := cli(t, project, "sync", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    f.shell("grep -q 'exit 7' \"$1/runs/$2/work/train.sh\"; grep -q changed \"$1/cache/train.sh\"; test \"$(wc -l <\"$1/runs/$2/work/count\")\" -eq 1", submitted.RemoteDir, submitted.ID)
    if code, out := cli(t, project, "add", "other", "--ssh", "missing-target", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    if code, out := cli(t, project, "use", "other", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    bound := runCLI(t, project, "ps", submitted.ID, "--json")
    if bound.Target != "lab" || bound.Status != "running" {
        t.Fatal(bound)
    }
    f.shell("touch \"$1/runs/$2/work/release\"", submitted.RemoteDir, submitted.ID)
    var finished execution.View
    wait(t, 5*time.Second, func() bool {
        finished = runCLI(t, project, "ps", submitted.ID, "--json")
        return finished.Status == "failed"
    })
    if finished.ExitCode == nil || *finished.ExitCode != 7 {
        t.Fatal(finished)
    }
    if code, out := cli(t, project, "use", "lab", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    sourceFile(t, project, "loaf.toml", "[run]\nsetup='exit 9'\n[run.env]\nPRIVATE_VALUE='not-in-observations'\n")
    setup := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "touch trained")
    wait(t, 5*time.Second, func() bool {
        setup = runCLI(t, project, "ps", setup.ID, "--json")
        return setup.Status == "failed"
    })
    if setup.Phase != "setup" || setup.ExitCode == nil || *setup.ExitCode != 9 {
        t.Fatal(setup)
    }
    _, out := cli(t, project, "ps", setup.ID, "--json")
    if strings.Contains(string(out), "not-in-observations") || strings.Contains(string(out), "PRIVATE_VALUE") {
        t.Fatal(string(out))
    }
    f.shell("test ! -e \"$1/runs/$2/work/trained\"", setup.RemoteDir, setup.ID)
}

func TestPreparedLaunchClaimsAndStopDuringSetup(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    bootstrap := execution.Bootstrap{Remote: f.client}
    report, err := f.client.Preflight(ctx, f.target, "~/prepared", true)
    if err != nil {
        t.Fatal(err)
    }
    info, err := bootstrap.Verify(ctx, f.target, report.Arch, true)
    if err != nil {
        t.Fatal(err)
    }
    // Native rsync receives and prepares the run before either activation.
    project := t.TempDir()
    sourceFile(t, project, "source.txt", "original")
    sender := ssh.Rsync{Client: f.client, Binary: "rsync"}
    root := "/home/researcher/prepared 中文"
    input := ssh.SyncInput{Root: root, ProjectID: "project", RunID: "claim", Mode: "mirror", Paths: []string{"source.txt"}}
    if _, err := sender.Sync(ctx, f.target, info.Path, project, input, true); err != nil {
        t.Fatal(err)
    }
    if _, err := sender.Sync(ctx, f.target, info.Path, project, input, true); err == nil {
        t.Fatal("receiver copy failure was hidden by native rsync")
    }
    // Repair the cache dirtied by failed finalization without altering the copy.
    repair := input
    repair.RunID = ""
    if _, err := sender.Sync(ctx, f.target, info.Path, project, repair, true); err != nil {
        t.Fatal(err)
    }
    f.shell(`printf changed >"$1/cache/source.txt"`, root)
    request := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "launch", Root: root, ProjectID: "project", RunID: "claim", Args: []string{"/bin/sh", "-c", "echo once >> count; while [ ! -f release ]; do sleep .05; done"}}
    var wg sync.WaitGroup
    errs := make(chan error, 2)
    for i := 0; i < 2; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, err := f.client.Control(ctx, f.target, info.Path, request, true)
            errs <- err
        }()
    }
    wg.Wait()
    close(errs)
    for err := range errs {
        if err != nil && err.Error() != "SUBMISSION_UNKNOWN" {
            t.Fatal(err)
        }
    }
    f.shell(`test "$(cat "$1/runs/claim/work/source.txt")" = original; test "$(wc -l <"$1/runs/claim/work/count")" -eq 1; touch "$1/runs/claim/work/release"`, root)
    input.RunID = "setup-stop"
    if _, err := sender.Sync(ctx, f.target, info.Path, project, input, true); err != nil {
        t.Fatal(err)
    }
    request.RunID = "setup-stop"
    request.Setup = "echo setup-ready; while [ ! -f release-setup ]; do sleep .05; done"
    request.Args = []string{"/bin/sh", "-c", "touch trained"}
    r, err := f.client.Control(ctx, f.target, info.Path, request, true)
    if err != nil || r.Phase != "setup" {
        t.Fatal(r, err)
    }
    request.Action = "stop"
    if _, err := f.client.Control(ctx, f.target, info.Path, request, true); err != nil {
        t.Fatal(err)
    }
    request.Action = "status"
    wait(t, 5*time.Second, func() bool {
        r, err = f.client.Control(ctx, f.target, info.Path, request, true)
        return err == nil && r.Status == "stopped"
    })
    f.shell(`test ! -e "$1/runs/setup-stop/work/trained"`, root)
}
