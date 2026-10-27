package integration

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"

    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

func TestRunnerStopGraceAndGroupCleanup(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    report, err := f.client.Preflight(ctx, f.target, "~/stopping", true)
    if err != nil {
        t.Fatal(err)
    }
    info, err := (execution.Bootstrap{Remote: f.client}).Verify(ctx, f.target, report.Arch, true)
    if err != nil {
        t.Fatal(err)
    }
    root := "/home/researcher/stopping 中文"
    f.managedRemote(root)
    invoke := func(action, id string, grace *int64, script string) *wire.RunRecord {
        t.Helper()
        req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: action, Root: root, ProjectID: "project", RunID: id, GraceMillis: grace}
        if script != "" {
            req.Args = []string{"/bin/sh", "-c", script}
        }
        r, err := f.client.Control(ctx, f.target, info.Path, req, true)
        if err != nil {
            t.Fatal(action, id, err)
        }
        return r
    }
    ready := func(id, file string) {
        t.Helper()
        wait(t, 5*time.Second, func() bool {
            return exec.CommandContext(ctx, "docker", "exec", f.id, "test", "-s", root+"/runs/"+id+"/work/"+file).Run() == nil
        })
    }
    finished := func(id string) *wire.RunRecord {
        t.Helper()
        var r *wire.RunRecord
        wait(t, 5*time.Second, func() bool {
            r = invoke("status", id, nil, "")
            return wire.Terminal(r.Status)
        })
        return r
    }

    invoke("start", "cleanup", nil, `trap 'sleep .4; echo cleaned >cleaned; exit 23' TERM; echo ready >ready; while :; do sleep .02; done`)
    ready("cleanup", "ready")
    grace := int64(1000)
    first := invoke("stop", "cleanup", &grace, "")
    zero := int64(0)
    duplicate := invoke("stop", "cleanup", &zero, "")
    if first.StopGraceMillis == nil || duplicate.StopGraceMillis == nil || *first.StopGraceMillis != grace || *duplicate.StopGraceMillis != grace {
        t.Fatal("duplicate stop changed grace", first, duplicate)
    }
    r := finished("cleanup")
    if r.Status != "stopped" || r.ExitCode == nil || *r.ExitCode != 23 || r.Signal != "" {
        t.Fatalf("cleanup did not complete in grace: %+v", r)
    }
    f.shell(`test -s "$1/runs/cleanup/work/cleaned"`, root)
    before := f.remoteRecord(root, "cleanup")
    invoke("stop", "cleanup", &zero, "")
    if after := f.remoteRecord(root, "cleanup"); after != before {
        t.Fatal("duplicate stop rewrote terminal result")
    }

    command(t, "docker", "exec", "-d", "--user", "researcher", f.id, "/bin/sh", "-c", `echo $$ >"$1/sentinel.pid"; exec sleep 60`, "--", root)
    invoke("start", "children", nil, `trap 'exit 0' TERM; sh -c 'trap "" TERM; echo $$ >worker.pid; while :; do sleep .05; done' & wait`)
    ready("children", "worker.pid")
    grace = 100
    invoke("stop", "children", &grace, "")
    r = finished("children")
    if r.Status != "stopped" || r.ExitCode == nil || *r.ExitCode != 0 {
        t.Fatalf("lost leader result: %+v", r)
    }
    f.shell(`worker=$(cat "$1/runs/children/work/worker.pid"); if [ -e "/proc/$worker/stat" ]; then test "$(awk '{print $3}' "/proc/$worker/stat")" = Z; fi; kill -0 "$(cat "$1/sentinel.pid")"`, root)
}

func stopProject(t *testing.T, f *fixture) string {
    t.Helper()
    project := t.TempDir()
    for _, args := range [][]string{{"machine", "add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"machine", "use", "lab", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    return project
}

func waitRunFile(t *testing.T, f *fixture, run execution.View, file string) {
    t.Helper()
    wait(t, 5*time.Second, func() bool {
        return exec.Command("docker", "exec", f.id, "test", "-s", run.RemoteDir+"/runs/"+run.ID+"/"+file).Run() == nil
    })
}

func stopCLICommand(ctx context.Context, project string, args ...string) *exec.Cmd {
    cmd := exec.CommandContext(ctx, os.Getenv("LOAF_BIN"), args...)
    cmd.Dir = project
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(project, "config"), "XDG_STATE_HOME="+filepath.Join(project, "state"), "LOAF_LANG=en")
    return cmd
}

func TestPublicStopWorkflow(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    sourceFile(t, project, "loaf.toml", "[stop]\ngrace = '1s'\n")
    started := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", `trap 'sleep .4; echo cleaned; exit 23' TERM; echo ready >ready; while :; do sleep .02; done`)
    waitRunFile(t, f, started, "work/ready")
    cfg := filepath.Join(project, "config", "loaf", "config.toml")
    data, err := os.ReadFile(cfg)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(cfg, append(data, []byte("\n[stop]\ngrace = '0s'\n")...), 0600); err != nil {
        t.Fatal(err)
    }
    // A different default connection and directory must not redirect stop.
    for _, args := range [][]string{{"machine", "add", "other", "--ssh", "ssh -p 9 nobody@127.0.0.1", "--json"}, {"machine", "use", "other", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    sourceFile(t, project, "loaf.toml", "remote_dir = '/home/researcher/not-this-task'\n[stop]\ngrace = '1s'\n")
    stopped := runCLI(t, project, "stop", "--json")
    if stopped.ID != started.ID || stopped.Target != "lab" || stopped.Status != "stopped" || stopped.ExitCode == nil || *stopped.ExitCode != 23 || stopped.StopGraceMillis == nil || *stopped.StopGraceMillis != 1000 {
        t.Fatalf("public stop: %+v", stopped)
    }
    before := f.remoteRecord(started.RemoteDir, started.ID)
    repeated := runCLI(t, project, "stop", started.ID, "--grace", "0s", "--json")
    if repeated.Status != stopped.Status || f.remoteRecord(started.RemoteDir, started.ID) != before {
        t.Fatal("repeated stop changed terminal result")
    }
    logs := runCLI(t, project, "logs", started.ID, "--json")
    if logs.Log == nil || !strings.Contains(logs.Log.Text, "cleaned") {
        t.Fatal("cleanup log lost", logs)
    }
    if code, out := cli(t, project, "machine", "use", "lab", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    sourceFile(t, project, "loaf.toml", "[run]\nsetup = 'echo ready >ready; while :; do sleep .02; done'\n[stop]\ngrace = '1s'\n")
    setup := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "touch trained")
    waitRunFile(t, f, setup, "work/ready")
    stopped = runCLI(t, project, "stop", setup.ID, "--grace", "0s", "--json")
    if stopped.Status != "stopped" || stopped.Phase != "setup" || stopped.StopGraceMillis == nil || *stopped.StopGraceMillis != 0 {
        t.Fatal("setup stop failed", stopped)
    }
    f.shell(`test ! -e "$1/runs/$2/work/trained"`, setup.RemoteDir, setup.ID)

    sourceFile(t, project, "loaf.toml", "")
    orphaned := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", `sh -c 'trap "" TERM; echo $$ >worker.pid; while :; do sleep .05; done' & while [ ! -s worker.pid ]; do sleep .02; done; exit 7`)
    waitRunFile(t, f, orphaned, "work/worker.pid")
    var record wire.RunRecord
    if err := json.Unmarshal([]byte(f.remoteRecord(orphaned.RemoteDir, orphaned.ID)), &record); err != nil {
        t.Fatal(err)
    }
    wait(t, 5*time.Second, func() bool {
        return exec.Command("docker", "exec", f.id, "/bin/sh", "-c", `test "$(awk '{print $3}' "/proc/$1/stat")" = Z`, "--", fmt.Sprint(record.ChildPID)).Run() == nil
    })
    stopped = runCLI(t, project, "stop", orphaned.ID, "--grace", "0s", "--json")
    if stopped.Status != "stopped" || stopped.ExitCode == nil || *stopped.ExitCode != 7 || stopped.StopGraceMillis == nil || *stopped.StopGraceMillis != 0 {
        t.Fatal("stop ignored after natural leader exit", stopped)
    }
    f.shell(`worker=$(cat "$1/runs/$2/work/worker.pid"); if [ -e "/proc/$worker/stat" ]; then test "$(awk '{print $3}' "/proc/$worker/stat")" = Z; fi`, orphaned.RemoteDir, orphaned.ID)
    for i := 0; i < 4; i++ {
        natural := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "echo ready >ready; while [ ! -f release ]; do sleep .02; done; exit 7")
        waitRunFile(t, f, natural, "work/ready")
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        cmd := stopCLICommand(ctx, project, "stop", natural.ID, "--json")
        var out, diagnostic bytes.Buffer
        cmd.Stdout, cmd.Stderr = &out, &diagnostic
        if err := cmd.Start(); err != nil {
            cancel()
            t.Fatal(err)
        }
        f.shell(`touch "$1/runs/$2/work/release"`, natural.RemoteDir, natural.ID)
        err := cmd.Wait()
        cancel()
        var response struct {
            Data execution.View `json:"data"`
        }
        if err != nil || diagnostic.Len() != 0 || json.Unmarshal(out.Bytes(), &response) != nil || !wire.Terminal(response.Data.Status) {
            t.Fatal("natural-exit/stop race", out.String(), diagnostic.String(), err)
        }
        result := response.Data
        if result.Status == "failed" && (result.ExitCode == nil || *result.ExitCode != 7) || result.Status == "stopped" && result.Signal == "" && result.ExitCode == nil {
            t.Fatal("exit outcome lost", result)
        }
        before := f.remoteRecord(natural.RemoteDir, natural.ID)
        runCLI(t, project, "stop", natural.ID, "--json")
        if after := f.remoteRecord(natural.RemoteDir, natural.ID); before != after {
            t.Fatal("stop overwrote natural terminal record")
        }
    }
}

func TestCanceledStopKeepsAcceptedRequest(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    started := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", `trap 'while [ ! -f release-cleanup ]; do sleep .02; done; exit 19' TERM; echo ready >ready; while :; do sleep .02; done`)
    waitRunFile(t, f, started, "work/ready")
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    cmd := stopCLICommand(ctx, project, "stop", started.ID, "--grace", "2s", "--json")
    var out, diagnostic bytes.Buffer
    cmd.Stdout, cmd.Stderr = &out, &diagnostic
    if err := cmd.Start(); err != nil {
        t.Fatal(err)
    }
    wait(t, 5*time.Second, func() bool {
        return f.remoteSQL(started.RemoteDir, "SELECT count(*) FROM stop_requests WHERE run_id='"+started.ID+"'") == "1"
    })
    if err := cmd.Process.Signal(os.Interrupt); err != nil {
        t.Fatal(err)
    }
    err := cmd.Wait()
    var exit *exec.ExitError
    if !errors.As(err, &exit) || exit.ExitCode() != 3 || diagnostic.Len() != 0 || !bytes.Contains(out.Bytes(), []byte(`"code":"STOP_UNCONFIRMED"`)) || !bytes.Contains(out.Bytes(), []byte(started.ID)) {
        t.Fatal("canceled confirmation", err, out.String(), diagnostic.String())
    }
    f.shell(`touch "$1/runs/$2/work/release-cleanup"`, started.RemoteDir, started.ID)
    done := runCLI(t, project, "status", started.ID, "--wait", "5s", "--json")
    if done.Status != "stopped" || done.ExitCode == nil || *done.ExitCode != 19 {
        t.Fatal("accepted stop was withdrawn", done)
    }
}

func TestPublicStopRefusesLostSupervisor(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    started := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "echo ready >ready; while :; do sleep .05; done")
    waitRunFile(t, f, started, "work/ready")
    var record wire.RunRecord
    if err := json.Unmarshal([]byte(f.remoteRecord(started.RemoteDir, started.ID)), &record); err != nil {
        t.Fatal(err)
    }
    f.shell(`kill -KILL "$1"`, fmt.Sprint(record.SupervisorPID))
    wait(t, 5*time.Second, func() bool {
        observed := runCLI(t, project, "status", started.ID, "--json")
        return observed.Status == "unknown"
    })
    code, out := cli(t, project, "stop", started.ID, "--json")
    if code != 3 || !bytes.Contains(out, []byte(`"code":"STOP_UNCONFIRMED"`)) || !bytes.Contains(out, []byte(`"status":"unknown"`)) {
        t.Fatal(code, string(out))
    }
    if f.remoteSQL(started.RemoteDir, "SELECT count(*) FROM stop_requests WHERE run_id='"+started.ID+"'") != "0" {
        t.Fatal("stop accepted for a lost supervisor")
    }
    f.shell(`kill -0 "$1"`, fmt.Sprint(record.ChildPID))
}

func TestLostStopConfirmationQueriesWithoutReplaying(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    realSSH, err := exec.LookPath("ssh")
    if err != nil {
        t.Fatal(err)
    }
    bin := filepath.Join(f.dir, "stop-fault-bin")
    if err := os.Mkdir(bin, 0700); err != nil {
        t.Fatal(err)
    }
    counter := filepath.Join(bin, "requests")
    marker := filepath.Join(bin, "drop-observation")
    script := `#!/bin/sh
for argument do
    case "$argument" in *'__receive'*|*'/bin/sh -s --'*|*'umask 077'*|-V) exec ` + ssh.Quote(realSSH) + ` "$@" ;; esac
done
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT HUP INT TERM
cat >"$payload"
if grep -q '"action":"stop"' "$payload"; then
    printf 'stop\n' >>` + ssh.Quote(counter) + `
    ` + ssh.Quote(realSSH) + ` "$@" <"$payload" >/dev/null
    exit 255
fi
if [ -e ` + ssh.Quote(marker) + ` ] && grep -q '"action":"observe"' "$payload"; then
    ` + ssh.Quote(realSSH) + ` "$@" <"$payload" >/dev/null
    exit 255
fi
` + ssh.Quote(realSSH) + ` "$@" <"$payload"
exit $?
`
    if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0700); err != nil {
        t.Fatal(err)
    }
    originalPATH := os.Getenv("PATH")
    t.Setenv("PATH", bin+string(os.PathListSeparator)+originalPATH)
    for _, loseObservation := range []bool{false, true} {
        started := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "echo ready >ready; while :; do sleep .02; done")
        waitRunFile(t, f, started, "work/ready")
        if loseObservation {
            if err := os.WriteFile(marker, nil, 0600); err != nil {
                t.Fatal(err)
            }
        }
        code, out := cli(t, project, "stop", started.ID, "--json")
        if loseObservation {
            if code != 3 || !bytes.Contains(out, []byte(`"code":"STOP_UNCONFIRMED"`)) || bytes.Contains(out, []byte(`"status":"stopped"`)) {
                t.Fatal(code, string(out))
            }
            if err := os.Remove(marker); err != nil {
                t.Fatal(err)
            }
        } else if code != 0 || !bytes.Contains(out, []byte(`"status":"stopped"`)) {
            t.Fatal(code, string(out))
        }
        after := runCLI(t, project, "status", started.ID, "--json")
        if after.Status != "stopped" || after.StopGraceMillis == nil || *after.StopGraceMillis != wire.DefaultStopGrace.Milliseconds() {
            t.Fatal("original run was not stopped", after)
        }
    }
    calls, err := os.ReadFile(counter)
    if err != nil || strings.Count(string(calls), "stop") != 2 {
        t.Fatal("stop was automatically replayed", string(calls), err)
    }
}
