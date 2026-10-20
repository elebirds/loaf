package integration

import (
    "bufio"
    "bytes"
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

func TestProjectRunWaitSetupAndRecovery(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := t.TempDir()
    sourceFile(t, project, "loaf.toml", "[run]\nwait = '5s'\n[logs]\ntail = 100\n")
    sourceFile(t, project, "source.txt", "original")
    for _, args := range [][]string{{"add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"use", "lab", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    short := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "printf '中文 ready\\n'; exit 7")
    if short.Status != "failed" || short.ExitCode == nil || *short.ExitCode != 7 || short.Log == nil || short.Log.Text != "中文 ready\n" {
        t.Fatalf("short task: %+v", short)
    }
    literal := "space 中文 ; $(touch injected) 'quoted'"
    argv := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", `printf '%s' "$1"`, "--", literal)
    if argv.Status != "succeeded" || argv.Log == nil || argv.Log.Text != literal {
        t.Fatalf("argv: %+v", argv)
    }
    f.shell(`test ! -e "$1/runs/$2/work/injected"`, argv.RemoteDir, argv.ID)
    sourceFile(t, project, "loaf.toml", "[run]\nwait = '5s'\nsetup = \"export SETUP_ONLY=leaked; cd /; printf 'setup\\\\n'\"\ncommand = \"printf '%s|%s' \\\"$EXPERIMENT\\\" \\\"${SETUP_ONLY-unset}\\\"\"\n[run.env]\nEXPERIMENT = 'baseline'\nPRIVATE_VALUE = 'not-in-observations'\n")
    configured := runCLI(t, project, "run", "--json")
    if configured.Status != "succeeded" || configured.Log == nil || !strings.Contains(configured.Log.Text, "baseline|unset") {
        t.Fatalf("setup: %+v", configured)
    }
    _, out := cli(t, project, "ps", configured.ID, "--json")
    if bytes.Contains(out, []byte("not-in-observations")) || bytes.Contains(out, []byte("PRIVATE_VALUE")) {
        t.Fatalf("environment leaked: %s", out)
    }
    sourceFile(t, project, "loaf.toml", "[run]\nwait = '5s'\nsetup = 'echo setup-failed; exit 9'\n")
    failed := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "touch trained")
    if failed.Phase != "setup" || failed.Status != "failed" || failed.ExitCode == nil || *failed.ExitCode != 9 {
        t.Fatalf("setup failure: %+v", failed)
    }
    f.shell(`test ! -e "$1/runs/$2/work/trained"`, failed.RemoteDir, failed.ID)
    sourceFile(t, project, "loaf.toml", "[run]\nwait = '0s'\n")
    sourceFile(t, project, "long.sh", "echo once >> launch-count\necho ready\nwhile [ ! -f release ]; do sleep .05; done\necho final\nexit 7\n")
    long := runCLI(t, project, "run", "--wait", "200ms", "--json", "--", "/bin/sh", "long.sh")
    if long.Status != "running" || long.Log == nil || !strings.Contains(long.Log.Text, "ready") {
        t.Fatalf("long wait: %+v", long)
    }
    sourceFile(t, project, "long.sh", "echo changed\n")
    if code, out := cli(t, project, "sync", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    f.shell(`grep -q 'echo final' "$1/runs/$2/work/long.sh"; grep -q changed "$1/cache/long.sh"; test "$(wc -l <"$1/runs/$2/work/launch-count")" -eq 1`, long.RemoteDir, long.ID)

    // The default machine and TOML directory can change without redirecting logs.
    if code, out := cli(t, project, "add", "other", "--ssh", "ssh -F "+ssh.Quote(f.config)+" fixture", "--json"); code != 0 {
        t.Fatal(string(out))
    }
    if code, out := cli(t, project, "use", "other", "--json"); code != 0 {
        t.Fatal(string(out))
    }
    sourceFile(t, project, "loaf.toml", "remote_dir = '/home/researcher/foreign'\n")
    f.shell(`mkdir -p /home/researcher/foreign; echo keep >/home/researcher/foreign/data`)
    if code, out := cli(t, project, "run", "--json", "--", "/bin/true"); code != 1 {
        t.Fatal(code, string(out))
    }
    latest := runCLI(t, project, "logs", "--json")
    if latest.ID != long.ID || latest.Target != "lab" {
        t.Fatalf("latest changed on sync failure: %+v", latest)
    }

    // Cancel a real NDJSON observation after receiving a chunk. The run survives.
    streamCtx, cancelStream := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancelStream()
    cmd := exec.CommandContext(streamCtx, os.Getenv("LOAF_BIN"), "logs", long.ID, "-f", "--json")
    cmd.Dir = project
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(project, "config"), "XDG_STATE_HOME="+filepath.Join(project, "state"), "LOAF_LANG=en")
    pipe, err := cmd.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    var diagnostic bytes.Buffer
    cmd.Stderr = &diagnostic
    if err := cmd.Start(); err != nil {
        t.Fatal(err)
    }
    scanner := bufio.NewScanner(pipe)
    scanner.Buffer(make([]byte, 1024), 2<<20)
    if !scanner.Scan() || !json.Valid(scanner.Bytes()) {
        cancelStream()
        _ = cmd.Wait()
        t.Fatal("no JSON stream chunk", scanner.Err(), diagnostic.String())
    }
    if err := cmd.Process.Signal(os.Interrupt); err != nil {
        t.Fatal(err)
    }
    sawCanceled := false
    for scanner.Scan() {
        if bytes.Contains(scanner.Bytes(), []byte(`"code":"OBSERVATION_CANCELED"`)) {
            sawCanceled = true
        }
    }
    err = cmd.Wait()
    if !sawCanceled || diagnostic.Len() != 0 {
        t.Fatal("cancellation did not preserve JSON observation result", err, diagnostic.String())
    }
    observed := runCLI(t, project, "ps", long.ID, "--json")
    if observed.Status != "running" {
        t.Fatalf("log cancellation stopped training: %+v", observed)
    }
    f.shell(`touch "$1/runs/$2/work/release"`, long.RemoteDir, long.ID)
    done := runCLI(t, project, "ps", long.ID, "--wait", "5s", "--json")
    if done.Status != "failed" || done.ExitCode == nil || *done.ExitCode != 7 || done.Log == nil || !strings.Contains(done.Log.Text, "final") {
        t.Fatalf("retained result: %+v", done)
    }
    code, follow := cliNDJSON(t, project, "logs", long.ID, "-f", "--json")
    if code != 0 || !bytes.Contains(follow, []byte(`"type":"end"`)) || !bytes.Contains(follow, []byte("final")) {
        t.Fatal(code, string(follow))
    }
    t.Log("public packaged CLI: bounded wait, argv, setup failure, source isolation, target binding, canceled follow, retained exit and final NDJSON passed")
}

func cliNDJSON(t *testing.T, project string, args ...string) (int, []byte) {
    t.Helper()
    cmd := exec.Command(os.Getenv("LOAF_BIN"), args...)
    cmd.Dir = project
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(project, "config"), "XDG_STATE_HOME="+filepath.Join(project, "state"), "LOAF_LANG=en")
    out, err := cmd.Output()
    code := 0
    if err != nil {
        var exit *exec.ExitError
        if !errors.As(err, &exit) {
            t.Fatal(err)
        }
        code = exit.ExitCode()
    }
    decoder := json.NewDecoder(bytes.NewReader(out))
    for decoder.More() {
        var v any
        if err := decoder.Decode(&v); err != nil {
            t.Fatal(err, string(out))
        }
    }
    return code, out
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

func TestPackagedCPUExamples(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    packageDir := filepath.Dir(os.Getenv("LOAF_BIN"))
    if _, err := os.Stat(filepath.Join(packageDir, "skills", "loaf", "SKILL.md")); err != nil {
        t.Fatal("packaged Skill missing", err)
    }
    for _, example := range []string{"cpu", "setup"} {
        project := t.TempDir()
        if err := os.CopyFS(project, os.DirFS(filepath.Join(packageDir, "examples", example))); err != nil {
            t.Fatal(err)
        }
        for _, args := range [][]string{{"add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"use", "lab", "--json"}} {
            if code, out := cli(t, project, args...); code != 0 {
                t.Fatal(code, string(out))
            }
        }
        ran := runCLI(t, project, "run", "--json")
        if ran.Status != "succeeded" || ran.ExitCode == nil || *ran.ExitCode != 0 {
            t.Fatalf("example %s: %+v", example, ran)
        }
        f.shell(`test -d "$1/runs/$2/work/outputs"`, ran.RemoteDir, ran.ID)
        pulled := pullCLI(t, project, 0, ran.ID, "--json")
        name, want := "result.txt", "result=42\n"
        if example == "setup" {
            name, want = "experiment.txt", "baseline\n"
        }
        if data, err := os.ReadFile(filepath.Join(pulled.Destination, "outputs", name)); err != nil || string(data) != want {
            t.Fatal("packaged example output unusable", string(data), err)
        }
        if example == "cpu" {
            human := exec.Command(os.Getenv("LOAF_BIN"), "--lang", "zh-Hans", "run")
            human.Dir = project
            human.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(project, "config"), "XDG_STATE_HOME="+filepath.Join(project, "state"))
            var output, progress bytes.Buffer
            human.Stdout = &output
            human.Stderr = &progress
            if err := human.Run(); err != nil || !strings.Contains(progress.String(), "正在同步") || !strings.Contains(progress.String(), "远端已接管") || strings.Count(output.String(), "CPU example started") != 1 || !strings.Contains(output.String(), "成功") {
                t.Fatal(err, output.String(), progress.String())
            }
            t.Logf("human Chinese run output:\n%s%s", progress.String(), output.String())
        }
    }
}
