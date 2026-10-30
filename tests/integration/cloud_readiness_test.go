package integration

import (
    "context"
    "errors"
    "io"
    "net"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"

    application "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/ssh"
)

type readinessProvider struct{ ref cloud.Ref }

func (p readinessProvider) List(context.Context, int) (cloud.Page, error) { return cloud.Page{}, nil }
func (p readinessProvider) Get(context.Context, cloud.Ref) (cloud.Instance, error) {
    return cloud.Instance{Ref: p.ref, Status: "running"}, nil
}
func (p readinessProvider) Connection(context.Context, cloud.Ref) (cloud.Connection, error) {
    return cloud.Connection{}, nil
}
func (p readinessProvider) Create(context.Context, cloud.Plan, *cloud.Offer) (cloud.Ref, error) {
    panic("readiness must not allocate")
}
func (p readinessProvider) Start(context.Context, cloud.Ref) error {
    panic("readiness must not power on")
}
func (p readinessProvider) Stop(context.Context, cloud.Ref) error {
    panic("readiness must not power off")
}

func TestCloudNativeReadinessDiagnostics(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    original, err := os.ReadFile(f.known)
    if err != nil {
        t.Fatal(err)
    }
    probe := func(target config.Target, want, reason string) {
        t.Helper()
        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer cancel()
        _, err := f.client.Exec(ctx, target, ssh.ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
        if err == nil || ssh.FailureKind(err) != want || reason != "" && ssh.FailureReason(err) != reason || strings.Contains(err.Error(), "127.0.0.1") {
            t.Fatal(want, ssh.FailureKind(err), ssh.FailureReason(err), err)
        }
    }
    if err := os.WriteFile(f.known, nil, 0600); err != nil {
        t.Fatal(err)
    }
    probe(f.target, "host_trust", "host_key_unknown")
    wrong := filepath.Join(f.dir, "wrong-key")
    command(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", wrong)
    pub, _ := os.ReadFile(wrong + ".pub")
    changed := "[127.0.0.1]:" + f.port + " " + string(pub) + "\n"
    _ = os.WriteFile(f.known, []byte(changed), 0600)
    probe(f.target, "host_trust", "host_key_changed")
    retained, _ := os.ReadFile(f.known)
    if string(retained) != changed {
        t.Fatal("changed key was removed")
    }
    _ = os.WriteFile(f.known, original, 0600)
    bad := f.target
    badConfig := filepath.Join(f.dir, "bad-config")
    content, e := os.ReadFile(f.config)
    if e != nil {
        t.Fatal(e)
    }
    if e := os.WriteFile(badConfig, []byte(strings.ReplaceAll(string(content), filepath.ToSlash(f.key), filepath.ToSlash(wrong))), 0600); e != nil {
        t.Fatal(e)
    }
    bad.Args = []string{"-F", badConfig}
    probe(bad, "authentication", "authentication")
    // A reserved, then closed local port gives a real refusal. Open a forwarding
    // proxy later; the same cloud binding must become ready on its second probe.
    reserved, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatal(err)
    }
    address := reserved.Addr().String()
    _, port, _ := net.SplitHostPort(address)
    reserved.Close()
    target := config.Target{Destination: "researcher@127.0.0.1", Args: []string{"-F", f.config, "-p", port}, RemoteBase: "~/projects"}
    probe(target, "connection_refused", "connection_refused")
    // Use the host key read directly from our own container, not keyscan or an
    // insecure acceptance policy, for the new fixture forwarding endpoint.
    hostKey := command(t, "docker", "exec", f.id, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")
    _ = os.WriteFile(f.known, append(original, []byte("[127.0.0.1]:"+port+" "+hostKey+"\n")...), 0600)
    proxyReady := make(chan net.Listener, 1)
    startProxy := make(chan struct{})
    go func() {
        <-startProxy
        listener, e := net.Listen("tcp", address)
        if e != nil {
            proxyReady <- nil
            return
        }
        proxyReady <- listener
        for {
            incoming, e := listener.Accept()
            if e != nil {
                return
            }
            go func() {
                defer incoming.Close()
                out, e := net.Dial("tcp", "127.0.0.1:"+f.port)
                if e != nil {
                    return
                }
                defer out.Close()
                go func() { _, _ = io.Copy(out, incoming) }()
                _, _ = io.Copy(incoming, out)
            }()
        }
    }()
    ref := cloud.Ref{Provider: "autodl", Product: "standard", Account: "fixture", ID: "same-instance"}
    account := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Env: "FIXTURE_TOKEN"}}
    store := &config.Store{ConfigFile: filepath.Join(f.dir, "settings.toml"), StateDir: filepath.Join(f.dir, "state")}
    settings := config.Global{}
    settings.Cloud.Accounts = map[string]cloud.Account{"fixture": account}
    settings.Cloud.DefaultAccount = "fixture"
    operations := f.client
    operations.Resolve = func(context.Context, config.Target) (config.Target, string, error) { return target, "", nil }
    resources := &application.Resources{Config: store, Settings: settings, Operations: operations, Cloud: &cloud.Service{Open: func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        return readinessProvider{ref}, nil
    }}}
    probes := 0
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    proxyStarted := false
    result, err := resources.CloudBind(ctx, application.CloudBindInput{Name: "test", Instance: ref.ID, ReadyTimeout: 8 * time.Second, Progress: func(e cloud.Event) error {
        probes = max(probes, e.ProbeAttempts)
        if !proxyStarted && e.FailureKind == "connection_refused" {
            close(startProxy)
            proxyStarted = true
        }
        return nil
    }})
    if !proxyStarted {
        close(startProxy)
    }
    listener := <-proxyReady
    if listener != nil {
        defer listener.Close()
    }
    if err != nil || result.Status != "ready" || probes != 2 {
        t.Fatal(result, err, probes)
    }
    // Cancellation and a stalled SSH banner are bounded; neither is a replay.
    stalled, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatal(err)
    }
    defer stalled.Close()
    done := make(chan struct{})
    go func() {
        defer close(done)
        c, e := stalled.Accept()
        if e == nil {
            defer c.Close()
            <-ctx.Done()
        }
    }()
    _, stallPort, _ := net.SplitHostPort(stalled.Addr().String())
    target.Args = []string{"-F", f.config, "-p", stallPort}
    short, stop := context.WithTimeout(context.Background(), 150*time.Millisecond)
    _, err = operations.Exec(short, config.Target{Cloud: &cloud.Binding{Ref: ref, Settings: account}}, ssh.ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
    stop()
    if !errors.Is(err, context.DeadlineExceeded) || ssh.FailureKind(err) != "observation_timeout" {
        t.Fatal(err, ssh.FailureKind(err))
    }
    cancel()
    <-done
}

func TestCloudNativeFirstTrustRequiresFingerprintDecision(t *testing.T) {
    binary := os.Getenv("LOAF_HOST_SSH_TEST")
    if os.Getenv("LOAF_INTEGRATION") != "1" || binary == "" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    key := command(t, "docker", "exec", f.id, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")
    pub := filepath.Join(f.dir, "host.pub")
    if err := os.WriteFile(pub, []byte(key), 0600); err != nil {
        t.Fatal(err)
    }
    fingerprint := strings.Fields(command(t, "ssh-keygen", "-lf", pub))[1]
    if err := os.WriteFile(f.known, nil, 0600); err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, binary, "-test.run=^TestNativeCloudTrustTerminalChild$")
    cmd.Env = append(os.Environ(), "LOAF_NATIVE_TRUST_CONFIG="+f.config)
    master, waitTerminal := startTerminal(t, cmd)
    defer master.Close()
    chunks := make(chan string, 100)
    go func() {
        defer close(chunks)
        b := make([]byte, 4096)
        for {
            n, e := master.Read(b)
            if n > 0 {
                chunks <- string(b[:n])
            }
            if e != nil {
                return
            }
        }
    }()
    transcript := ""
    confirmed := false
    for chunk := range chunks {
        transcript += chunk
        if !confirmed && strings.Contains(transcript, "Are you sure you want to continue connecting") {
            if !strings.Contains(transcript, fingerprint) {
                t.Fatal("native prompt did not display the owned fixture key", transcript)
            }
            if content, _ := os.ReadFile(f.known); len(content) != 0 {
                t.Fatal("key accepted before decision")
            }
            _, _ = io.WriteString(master, "yes\n")
            confirmed = true
        }
    }
    if err := waitTerminal(); err != nil || !confirmed || !strings.Contains(transcript, "LOAF_NATIVE_TRUST_COMPLETED") {
        t.Fatal(err, transcript)
    }
    _, err := f.client.Exec(ctx, f.target, ssh.ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
    if err != nil {
        t.Fatal("strict authentication did not follow trust", err)
    }
}
