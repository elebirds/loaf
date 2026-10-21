package integration

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
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

func pullCLI(t *testing.T, project string, want int, args ...string) sources.PullResult {
    t.Helper()
    code, out := cli(t, project, append([]string{"pull"}, args...)...)
    var response struct {
        Data sources.PullResult `json:"data"`
    }
    if code != want || json.Unmarshal(out, &response) != nil || response.Data.RunID == "" {
        t.Fatal(code, string(out))
    }
    return response.Data
}

func TestPublicPullWorkflow(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    sourceFile(t, project, "loaf.toml", "[outputs]\npaths=['outputs','checkpoints']\n")
    started := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs checkpoints; printf metrics >'outputs/中文 space.json'; printf literal >'outputs/back\slash[1]*?'; printf weights >checkpoints/model.bin; ln -s '中文 space.json' outputs/latest; exit 7`)
    if started.Status != "failed" {
        t.Fatal(started)
    }
    first := pullCLI(t, project, 0, "--json")
    if first.ChangedFiles == nil || *first.ChangedFiles != 4 || filepath.Base(first.Destination) != started.ID {
        t.Fatal(first)
    }
    sourceFile(t, first.Destination, "notes.txt", "keep local")
    repeated := pullCLI(t, project, 0, "--json")
    if repeated.ChangedFiles == nil || *repeated.ChangedFiles != 0 {
        t.Fatal(repeated)
    }
    before := f.shell(`cat "$1/runs/$2/run.json"`, started.RemoteDir, started.ID)
    for _, args := range [][]string{{"add", "other", "--ssh", "ssh -p 9 nobody@127.0.0.1", "--json"}, {"use", "other", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    sourceFile(t, project, "loaf.toml", "remote_dir='/home/researcher/not-this-run'\n[outputs]\npaths=['not-this-output']\n[pull]\nbase_dir='retrieved'\nchecksum=true\n")
    moved := pullCLI(t, project, 0, "--checksum=false", "--json")
    if moved.RunID != started.ID || moved.Target != "lab" || !reflect.DeepEqual(moved.Paths, []string{"outputs", "checkpoints"}) || moved.Policy.Checksum || filepath.Base(filepath.Dir(moved.Destination)) != "retrieved" {
        t.Fatal(moved)
    }
    for _, language := range []string{"en", "zh-Hans"} {
        exact := filepath.Join(project, "export "+language)
        result := pullCLI(t, project, 0, started.ID, "--lang", language, "--path", "checkpoints/model.bin", "--to", exact, "--json")
        canonical, _ := filepath.EvalSymlinks(exact)
        if result.Destination != canonical || !result.Policy.Checksum || !reflect.DeepEqual(result.Paths, []string{"checkpoints/model.bin"}) {
            t.Fatal(result)
        }
        if data, err := os.ReadFile(filepath.Join(exact, "checkpoints/model.bin")); err != nil || string(data) != "weights" {
            t.Fatal(string(data), err)
        }
        if _, err := os.Stat(filepath.Join(exact, "outputs")); !errors.Is(err, os.ErrNotExist) {
            t.Fatal("downloaded an unselected output", err)
        }
    }
    literal := pullCLI(t, project, 0, "--path", `outputs/back\slash[1]*?`, "--to", "literal-export", "--json")
    if data, err := os.ReadFile(filepath.Join(literal.Destination, `outputs/back\slash[1]*?`)); err != nil || string(data) != "literal" {
        t.Fatal("interpreted a literal output path", string(data), err)
    }
    f.shell(`cp -p "$1/runs/$2/work/checkpoints/model.bin" "$1/timestamp"; printf updated >"$1/runs/$2/work/checkpoints/model.bin"; touch -r "$1/timestamp" "$1/runs/$2/work/checkpoints/model.bin"`, started.RemoteDir, started.ID)
    quick := pullCLI(t, project, 0, "--checksum=false", "--json")
    strict := pullCLI(t, project, 0, "--json")
    if quick.ChangedFiles == nil || *quick.ChangedFiles != 0 || strict.ChangedFiles == nil || *strict.ChangedFiles != 1 {
        t.Fatal(quick, strict)
    }
    missing := pullCLI(t, project, 1, "--path", "outputs", "--path", "missing", "--json")
    if !reflect.DeepEqual(missing.MissingPaths, []string{"missing"}) || !reflect.DeepEqual(missing.DownloadedPaths, []string{"outputs"}) || missing.ChangedFiles == nil {
        t.Fatal(missing)
    }
    allMissing := filepath.Join(project, "all-missing")
    pullCLI(t, project, 1, "--path", "missing", "--to", allMissing, "--json")
    if _, err := os.Stat(allMissing); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("all-missing download created a destination", err)
    }
    f.shell(`ln -s /etc/passwd "$1/runs/$2/work/outputs/escape"`, started.RemoteDir, started.ID)
    unsafe := filepath.Join(project, "unsafe")
    code, out := cli(t, project, "pull", "--path", "outputs", "--to", unsafe, "--json")
    if code != 1 || !bytes.Contains(out, []byte(`"code":"UNSAFE_PATH"`)) {
        t.Fatal(code, string(out))
    }
    if _, err := os.Stat(unsafe); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("unsafe source wrote a destination", err)
    }
    f.shell(`rm "$1/runs/$2/work/outputs/escape"`, started.RemoteDir, started.ID)
    f.shell(`mkdir "$1/runs/$2/work/outputs/unreadable"; chmod 000 "$1/runs/$2/work/outputs/unreadable"`, started.RemoteDir, started.ID)
    code, out = cli(t, project, "pull", "--path", "outputs", "--json")
    if code != 1 || !bytes.Contains(out, []byte(`"code":"OUTPUT_READ_FAILED"`)) {
        t.Fatal("unreadable directory was not reported", code, string(out))
    }
    f.shell(`chmod 700 "$1/runs/$2/work/outputs/unreadable"; rmdir "$1/runs/$2/work/outputs/unreadable"`, started.RemoteDir, started.ID)
    outside := t.TempDir()
    local := filepath.Join(project, "linked-destination")
    if err := os.Mkdir(local, 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.Symlink(outside, filepath.Join(local, "outputs")); err != nil {
        t.Fatal(err)
    }
    code, out = cli(t, project, "pull", "--path", "outputs", "--to", local, "--json")
    if code != 1 || !bytes.Contains(out, []byte(`"code":"UNSAFE_PATH"`)) {
        t.Fatal(code, string(out))
    }
    entries, err := os.ReadDir(outside)
    if err != nil || len(entries) != 0 {
        t.Fatal("wrote outside local destination", entries, err)
    }
    if after := f.shell(`cat "$1/runs/$2/run.json"`, started.RemoteDir, started.ID); after != before {
        t.Fatal("pull changed execution record")
    }
    if data, err := os.ReadFile(filepath.Join(first.Destination, "notes.txt")); err != nil || string(data) != "keep local" {
        t.Fatal("deleted local extra", err)
    }
}

func TestInterruptedPullDoesNotStopTrainingOrBlockSyncAndStop(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    started := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", `mkdir outputs; dd if=/dev/urandom of=outputs/model.bin bs=1048576 count=4 2>/dev/null; echo ready >ready; while :; do sleep .05; done`)
    waitRunFile(t, f, started, "work/ready")
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Fatal(err)
    }
    slowDir := t.TempDir()
    sourceFile(t, slowDir, "rsync", "#!/bin/sh\nexec "+ssh.Quote(binary)+" --bwlimit=128 \"$@\"\n")
    if err := os.Chmod(filepath.Join(slowDir, "rsync"), 0700); err != nil {
        t.Fatal(err)
    }
    destination := filepath.Join(project, "loaf-results", started.ID)
    sourceFile(t, destination, "outputs/model.bin", "previous complete file")
    sourceFile(t, destination, "notes.txt", "keep")
    interrupted := func(stop bool) {
        t.Helper()
        ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
        defer cancel()
        cmd := stopCLICommand(ctx, project, "pull", started.ID, "--json")
        cmd.Env = append(cmd.Env, "PATH="+slowDir+":"+os.Getenv("PATH"))
        var out, diagnostic bytes.Buffer
        cmd.Stdout, cmd.Stderr = &out, &diagnostic
        if err := cmd.Start(); err != nil {
            t.Fatal(err)
        }
        done := make(chan struct{})
        var waitErr error
        go func() { waitErr = cmd.Wait(); close(done) }()
        t.Cleanup(func() { cancel(); <-done })
        wait(t, 8*time.Second, func() bool {
            select {
            case <-done:
                t.Fatalf("pull exited before opening a native temporary file: %v %s %s", waitErr, out.String(), diagnostic.String())
            default:
            }
            entries, _ := os.ReadDir(filepath.Join(destination, "outputs"))
            for _, entry := range entries {
                if strings.HasPrefix(entry.Name(), ".") {
                    // openrsync can buffer file contents while its temporary
                    // file is still size zero. Creation establishes readiness.
                    return true
                }
            }
            return false
        })
        if code, result := cli(t, project, "pull", started.ID, "--json"); code != 1 || !bytes.Contains(result, []byte(`"code":"PULL_BUSY"`)) {
            t.Fatal("overlapping destination not rejected", code, string(result))
        }
        if stop {
            stopped := runCLI(t, project, "stop", started.ID, "--grace", "0s", "--json")
            if stopped.Status != "stopped" {
                t.Fatal("download blocked stop", stopped)
            }
        } else {
            sourceFile(t, project, "new-source.txt", "new code")
            if code, result := cli(t, project, "sync", "--json"); code != 0 {
                t.Fatal("download blocked source sync", code, string(result))
            }
        }
        if err := cmd.Process.Signal(os.Interrupt); err != nil {
            t.Fatal(err)
        }
        select {
        case <-done:
            var exit *exec.ExitError
            if !errors.As(waitErr, &exit) || exit.ExitCode() != 1 || diagnostic.Len() != 0 || !json.Valid(out.Bytes()) || !bytes.Contains(out.Bytes(), []byte(`"code":"PULL_CANCELED"`)) {
                t.Fatal(waitErr, out.String(), diagnostic.String())
            }
        case <-time.After(5 * time.Second):
            t.Fatal("canceled pull did not exit")
        }
        if data, err := os.ReadFile(filepath.Join(destination, "outputs/model.bin")); err != nil || string(data) != "previous complete file" {
            t.Fatal("interrupted transfer replaced complete file", err)
        }
        entries, err := os.ReadDir(filepath.Join(destination, "outputs"))
        if err != nil || len(entries) != 1 || entries[0].Name() != "model.bin" {
            t.Fatal("canceled native transfer retained temporary files", entries, err)
        }
        wait(t, 5*time.Second, func() bool {
            return exec.Command("docker", "exec", f.id, "/bin/sh", "-c", "! pgrep -u researcher -x rsync").Run() == nil
        })
    }
    interrupted(false)
    if observed := runCLI(t, project, "ps", started.ID, "--json"); observed.Status != "running" {
        t.Fatal("pull cancellation stopped training", observed)
    }
    interrupted(true)
    result := pullCLI(t, project, 0, started.ID, "--json")
    if result.ChangedFiles == nil || *result.ChangedFiles != 1 {
        t.Fatal("retry failed", result)
    }
    if info, err := os.Stat(filepath.Join(destination, "outputs/model.bin")); err != nil || info.Size() != 4*1024*1024 {
        t.Fatal(info, err)
    }
    if code, out := cli(t, project, "sync", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    f.shell(`test ! -e "$1/cache/loaf-results"`, started.RemoteDir)
}
