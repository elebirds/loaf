package integration

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

type fixture struct {
    t                                 *testing.T
    id, port, dir, key, config, known string
    client                            *ssh.Client
    target                            config.Target
}

func command(t *testing.T, name string, args ...string) string {
    t.Helper()
    out, err := exec.Command(name, args...).CombinedOutput()
    if err != nil {
        t.Fatalf("%s %v: %v\n%s", name, args, err, out)
    }
    return strings.TrimSpace(string(out))
}

func newFixture(t *testing.T) *fixture {
    t.Helper()
    f := &fixture{t: t, dir: t.TempDir(), client: &ssh.Client{Binary: "ssh"}}
    keyDir := filepath.Join(f.dir, "SSH 中文 config")
    if err := os.Mkdir(keyDir, 0700); err != nil {
        t.Fatal(err)
    }
    f.key = filepath.Join(keyDir, "identity")
    f.config = filepath.Join(f.dir, "config")
    f.known = filepath.Join(keyDir, "known_hosts")
    command(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", f.key)
    f.id = command(t, "docker", "run", "-d", "--init", "--rm", "--tmpfs", "/tmp/loaf-full:rw,size=1m,mode=0777", "-p", "127.0.0.1::22", "loaf-sshd-test:local")
    t.Cleanup(func() {
        if t.Failed() {
            out, err := exec.Command("docker", "logs", f.id).CombinedOutput()
            t.Logf("fixture sshd logs (%v):\n%s", err, out)
        }
        if out, err := exec.Command("docker", "rm", "-f", f.id).CombinedOutput(); err != nil {
            t.Errorf("fixture cleanup: %v %s", err, out)
        }
    })
    // Linux bind mounts preserve host UIDs, which may fail sshd's StrictModes.
    command(t, "docker", "cp", f.key+".pub", f.id+":/home/researcher/.ssh/authorized_keys")
    command(t, "docker", "exec", f.id, "chown", "researcher:researcher", "/home/researcher/.ssh/authorized_keys")
    command(t, "docker", "exec", f.id, "chmod", "0600", "/home/researcher/.ssh/authorized_keys")
    address := command(t, "docker", "port", f.id, "22/tcp")
    _, port, err := net.SplitHostPort(address)
    if err != nil {
        t.Fatal(err)
    }
    f.port = port
    wait(t, 5*time.Second, func() bool {
        c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
        if err != nil {
            return false
        }
        defer c.Close()
        _ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
        banner := make([]byte, 64)
        n, err := c.Read(banner)
        return err == nil && strings.HasPrefix(string(banner[:n]), "SSH-")
    })
    hostKey := command(t, "docker", "exec", f.id, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")
    if err := os.WriteFile(f.known, []byte("[127.0.0.1]:"+port+" "+hostKey+"\n127.0.0.1 "+hostKey+"\n"), 0600); err != nil {
        t.Fatal(err)
    }
    contents := fmt.Sprintf("Host fixture\n    HostName 127.0.0.1\n    Port %s\nHost jumped\n    HostName 127.0.0.1\n    Port 22\n    ProxyJump fixture\nHost *\n    User researcher\n    IdentityFile %q\n    IdentityAgent none\n    IdentitiesOnly yes\n    UserKnownHostsFile %q\n    GlobalKnownHostsFile /dev/null\n    StrictHostKeyChecking yes\n    ControlMaster no\n", port, f.key, f.known)
    if err := os.WriteFile(f.config, []byte(contents), 0600); err != nil {
        t.Fatal(err)
    }
    f.target = config.Target{Destination: "fixture", Args: []string{"-F", f.config}, RemoteBase: "~/projects"}
    return f
}

func wait(t *testing.T, timeout time.Duration, ready func() bool) {
    t.Helper()
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if ready() {
            return
        }
        time.Sleep(30 * time.Millisecond)
    }
    t.Fatal("timed out waiting for observable readiness")
}

func (f *fixture) shell(script string, args ...string) string {
    f.t.Helper()
    argv := []string{"exec", "--user", "researcher", f.id, "/bin/sh", "-c", script, "--"}
    return command(f.t, "docker", append(argv, args...)...)
}

func cli(t *testing.T, dir string, args ...string) (int, []byte) {
    t.Helper()
    cmd := exec.Command(os.Getenv("LOAF_BIN"), args...)
    cmd.Dir = dir
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "XDG_STATE_HOME="+filepath.Join(dir, "state"), "LOAF_LANG=en")
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    out, err := cmd.Output()
    code := 0
    if err != nil {
        if e, ok := err.(*exec.ExitError); ok {
            code = e.ExitCode()
        } else {
            t.Fatal(err)
        }
    }
    if stderr.Len() != 0 {
        t.Fatalf("unexpected JSON stderr: %s", stderr.String())
    }
    if !json.Valid(out) {
        t.Fatalf("invalid JSON (%d): %s", code, out)
    }
    return code, out
}

func TestSSHFixtureKeyOwnership(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    const authorizedKeys = "/home/researcher/.ssh/authorized_keys"
    uid := command(t, "docker", "exec", f.id, "id", "-u", "researcher")
    if got := command(t, "docker", "exec", f.id, "stat", "-c", "%u:%a", authorizedKeys); got != uid+":600" {
        t.Fatalf("authorized_keys ownership and mode = %s, want %s:600", got, uid)
    }
    connect := func() error {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        _, err := f.client.Execute(ctx, f.target, "true", nil, true)
        return err
    }
    if err := connect(); err != nil {
        t.Fatalf("fixture authentication: %v", err)
    }

    // A Linux bind mount can retain a host UID that differs from the SSH user.
    // Keep the key readable to isolate StrictModes' ownership check.
    command(t, "docker", "exec", f.id, "chmod", "0644", authorizedKeys)
    command(t, "docker", "exec", f.id, "chown", "65534:65534", authorizedKeys)
    err := connect()
    var denied *fault.Error
    if !errors.As(err, &denied) || denied.Code != "SSH_FAILED" || !strings.Contains(fmt.Sprint(denied.Params["Detail"]), "Permission denied") {
        t.Fatalf("foreign-owned authorized_keys must reject authentication: %v", err)
    }
    logs := command(t, "docker", "logs", f.id)
    if !strings.Contains(logs, "bad ownership or modes for file "+authorizedKeys) {
        t.Fatalf("missing sshd ownership diagnostic:\n%s", logs)
    }
    command(t, "docker", "exec", f.id, "chown", "researcher:researcher", authorizedKeys)
    command(t, "docker", "exec", f.id, "chmod", "0600", authorizedKeys)
    if err := connect(); err != nil {
        t.Fatalf("restored fixture authentication: %v", err)
    }
}

func TestSSHAndDetachedRunner(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    project := t.TempDir()
    connection := "ssh -F " + ssh.Quote(f.config) + " -p " + f.port + " researcher@127.0.0.1"
    for _, args := range [][]string{{"add", "direct", "--ssh", connection, "--json"}, {"add", "alias", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"use", "alias", "--json"}, {"check", "--json"}, {"check", "--target", "direct", "--json"}} {
        code, out := cli(t, project, args...)
        if code != 0 {
            t.Fatalf("%v: %d %s", args, code, out)
        }
    }
    // A normal check must not install files on the server.
    f.shell(`test ! -e "$HOME/.local/share/loaf"`)
    jump := f.target
    jump.Destination = "jumped"
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    if _, err := f.client.Check(ctx, jump, "~/projects/test", true); err != nil {
        var detail *fault.Error
        if errors.As(err, &detail) {
            t.Fatalf("ProxyJump: %v %v", err, detail.Params)
        }
        t.Fatalf("ProxyJump: %v", err)
    }
    // Exercise a spaced -F path separately from OpenSSH's implicit jump command.
    spaced := filepath.Join(f.dir, "SSH 中文 config", "config")
    data, err := os.ReadFile(f.config)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(spaced, data, 0600); err != nil {
        t.Fatal(err)
    }
    spacedTarget := f.target
    spacedTarget.Args = []string{"-F", spaced}
    if _, err := f.client.Check(ctx, spacedTarget, "~/projects/test", true); err != nil {
        t.Fatal(err)
    }
    code, out := cli(t, project, "check", "--runner", "--timeout", "30s", "--json")
    if code != 0 {
        t.Fatalf("runner verification: %d %s", code, out)
    }
    var checked struct {
        Data struct {
            Runner execution.RunnerInfo `json:"runner"`
        }
    }
    if err := json.Unmarshal(out, &checked); err != nil || checked.Data.Runner.Path == "" || checked.Data.Runner.Version != wire.Version || checked.Data.Runner.Bytes == 0 {
        t.Fatal(string(out), err)
    }
    runner := checked.Data.Runner.Path
    root := "/home/researcher/project space 中文"
    f.shell(`mkdir -p "$1/cache"; printf '%s\n' project >"$1/.loaf-project"; printf '%s\n' "$2" >"$1/cache/train.sh"`, root, "echo tick; while [ ! -f release ]; do echo tick; sleep .05; done; echo done; exit 7")
    control := func(action, id string, args ...string) (*wire.RunRecord, error) {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: action, Root: root, ProjectID: "project", RunID: id, Args: args}
        if action == "stop" {
            grace := int64(50)
            req.GraceMillis = &grace
        }
        return f.client.Control(ctx, f.target, runner, req, true)
    }
    record, err := control("start", "completed", "/bin/sh", "train.sh")
    if err != nil || record.Status != "running" {
        t.Fatal(record, err)
    }
    // Every control call has already exited its SSH process. The command keeps running.
    record, err = control("status", "completed")
    if err != nil || record.Status != "running" {
        t.Fatal(record, err)
    }
    f.shell(`test -s "$1/runs/completed/output.log"; printf '%s' changed >"$1/cache/train.sh"; grep -q 'echo tick' "$1/runs/completed/work/train.sh"; touch "$1/runs/completed/work/release"`, root)
    wait(t, 5*time.Second, func() bool {
        record, err = control("status", "completed")
        return err == nil && record.Status == "failed"
    })
    if record.ExitCode == nil || *record.ExitCode != 7 {
        t.Fatal(record)
    }
    if !strings.Contains(f.shell(`cat "$1/runs/completed/output.log"`, root), "done") {
        t.Fatal("logs lost")
    }
    t.Logf("embedded runner %d bytes; isolated source copy %d ms; disconnected run exit=%d", checked.Data.Runner.Bytes, record.CopyMillis, *record.ExitCode)
    f.shell(`printf '%s\n' original >"$1/cache/data"`, root)
    reqArgs := []string{"/bin/sh", "-c", "echo once >> launch-count; while [ ! -f release ]; do sleep .05; done"}
    var wg sync.WaitGroup
    errs := make(chan error, 2)
    for n := 0; n < 2; n++ {
        wg.Add(1)
        go func() { defer wg.Done(); _, err := control("start", "duplicate", reqArgs...); errs <- err }()
    }
    wg.Wait()
    close(errs)
    for err := range errs {
        if err != nil && err.Error() != "SUBMISSION_UNKNOWN" {
            t.Fatal(err)
        }
    }
    f.shell(`test "$(wc -l <"$1/runs/duplicate/work/launch-count")" -eq 1; touch "$1/runs/duplicate/work/release"`, root)
    command(t, "docker", "exec", "-d", "--user", "researcher", f.id, "/bin/sh", "-c", `echo $$ >"$1/sentinel.pid"; exec sleep 60`, "--", root)
    record, err = control("start", "stoppable", "/bin/sh", "-c", `trap '' TERM; sh -c 'trap "" TERM; echo $$ > worker.pid; while :; do sleep .1; done' & wait`)
    if err != nil {
        t.Fatal(err)
    }
    wait(t, 5*time.Second, func() bool {
        return exec.Command("docker", "exec", f.id, "test", "-s", root+"/runs/stoppable/work/worker.pid").Run() == nil
    })
    worker := f.shell(`cat "$1/runs/stoppable/work/worker.pid"`, root)
    if _, err := control("stop", "stoppable"); err != nil {
        t.Fatal(err)
    }
    wait(t, 5*time.Second, func() bool {
        record, err = control("status", "stoppable")
        return err == nil && record.Status == "stopped"
    })
    if record.Signal == "" || record.ExitCode != nil {
        t.Fatalf("fabricated exit result: %+v", record)
    }
    f.shell(`if [ -e "/proc/$2/stat" ]; then test "$(awk '{print $3}' "/proc/$2/stat")" = Z; fi; kill -0 "$(cat "$1/sentinel.pid")"`, root, worker)
    repeat, err := control("stop", "stoppable")
    if err != nil || repeat.Status != "stopped" {
        t.Fatal(repeat, err)
    }
    for _, kind := range []string{"auth", "trust"} {
        data, err := os.ReadFile(f.config)
        if err != nil {
            t.Fatal(err)
        }
        if kind == "auth" {
            data = bytes.ReplaceAll(data, []byte(f.key), []byte(filepath.Join(f.dir, "missing-key")))
        } else {
            empty := filepath.Join(f.dir, "empty-known-hosts")
            if err := os.WriteFile(empty, nil, 0600); err != nil {
                t.Fatal(err)
            }
            data = bytes.ReplaceAll(data, []byte(f.known), []byte(empty))
        }
        broken := filepath.Join(f.dir, kind+"-config")
        if err := os.WriteFile(broken, data, 0600); err != nil {
            t.Fatal(err)
        }
        code, out := cli(t, project, "add", kind, "--ssh", "ssh -F "+ssh.Quote(broken)+" fixture", "--json")
        if code != 0 {
            t.Fatal(string(out))
        }
        code, out = cli(t, project, "check", "--target", kind, "--json")
        if code != 1 || !strings.Contains(string(out), `"code":"SSH_FAILED"`) {
            t.Fatal(code, string(out))
        }
    }
    wait(t, 5*time.Second, func() bool {
        return exec.Command("docker", "exec", f.id, "/bin/sh", "-c", "! pgrep -u researcher -x loaf-runner").Run() == nil
    })
    // Execute Linux-only process identity and protocol tests on the actual Linux kernel.
    remoteTest := os.Getenv("LOAF_REMOTE_TEST")
    if remoteTest != "" {
        command(t, "docker", "cp", remoteTest, f.id+":/tmp/remote.test")
        command(t, "docker", "exec", "--user", "researcher", f.id, "/tmp/remote.test", "-test.v")
    }
}
