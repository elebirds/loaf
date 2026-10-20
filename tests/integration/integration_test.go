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
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
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
    f.id = command(t, "docker", "run", "-d", "--init", "--rm", "-p", "127.0.0.1::22", "--mount", "type=bind,source="+f.key+".pub,target=/home/researcher/.ssh/authorized_keys,readonly", "loaf-sshd-test:local")
    t.Cleanup(func() {
        if out, err := exec.Command("docker", "rm", "-f", f.id).CombinedOutput(); err != nil {
            t.Errorf("fixture cleanup: %v %s", err, out)
        }
    })
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

func TestSSHConnectionsAndEnvironment(t *testing.T) {
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
}
