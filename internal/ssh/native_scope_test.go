package ssh

import (
    "bytes"
    "context"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/wire"
)

// Invoked against isolated fixtures by integration.sh with the host's native
// OpenSSH/rsync, and again inside Linux with native GNU rsync.
func TestNativePrivateConnectionWithRsync(t *testing.T) {
    runner := os.Getenv("LOAF_NATIVE_SCOPE_RUNNER")
    if runner == "" {
        t.Skip("run bash scripts/integration.sh")
    }
    scope := NewConnections()
    defer scope.Close()
    resolutions := 0
    cfg, dest := os.Getenv("LOAF_NATIVE_SCOPE_CONFIG"), os.Getenv("LOAF_NATIVE_SCOPE_DEST")
    if cfg == "" {
        cfg = "/tmp/linux-client/config"
    }
    if dest == "" {
        dest = "researcher@self"
    }
    trace := &Trace{}
    trace.Enable()
    client := &Client{Binary: "ssh", Connections: scope, Trace: trace, Resolve: func(_ context.Context, bound config.Target) (config.Target, string, error) {
        if bound.Cloud.Ref.ID != "native-linux-instance" {
            t.Error("redirected instance", bound.Cloud.Ref.ID)
        }
        resolutions++
        return config.Target{Destination: dest, Args: []string{"-F", cfg}}, "", nil
    }}
    target := config.Target{RemoteBase: "~/projects", Cloud: &cloud.Binding{
        Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "fixture", ID: "native-linux-instance"},
        Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "LOAF_NATIVE_SCOPE_TOKEN"}},
    }}
    ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
    defer cancel()
    if _, err := client.RunnerArch(ctx, target, true); err != nil {
        t.Fatal(err)
    }
    source, destination := t.TempDir(), t.TempDir()
    name := "source 中文 space.txt"
    if err := os.WriteFile(filepath.Join(source, name), []byte("native-linux-scope"), 0600); err != nil {
        t.Fatal(err)
    }
    payload := bytes.Repeat([]byte("native-transfer-integrity\n"), 32768)
    if err := os.WriteFile(filepath.Join(source, "payload.bin"), payload, 0600); err != nil {
        t.Fatal(err)
    }
    root := os.Getenv("LOAF_NATIVE_SCOPE_ROOT")
    if root == "" {
        root = filepath.Join(t.TempDir(), "managed project")
    }
    const projectID = "p_0123456789abcdef01234567"
    const runID = "r_0123456789abcdef01234567"
    sender := Rsync{Client: client, Binary: "rsync"}
    if _, err := sender.Sync(ctx, target, runner, source, SyncInput{Root: root, ProjectID: projectID, RunID: runID, Mode: "mirror", Paths: []string{name, "payload.bin"}}, true); err != nil {
        t.Fatal(err)
    }
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "launch", Root: root, ProjectID: projectID, RunID: runID, Args: []string{"/bin/sh", "-c", "mkdir outputs; cp " + Quote(name) + " outputs/a-result.txt; cp payload.bin outputs/z-payload.bin"}}
    if _, err := client.Control(ctx, target, runner, req, true); err != nil {
        t.Fatal(err)
    }
    req.WaitMillis = 5000
    observed, err := client.Observe(ctx, target, runner, req, true)
    if err != nil || observed.Record == nil || observed.Record.Status != "succeeded" {
        t.Fatal(observed, err)
    }
    for _, want := range []int{2, 0} {
        transfer, err := sender.Pull(ctx, target, runner, destination, PullInput{Root: root, ProjectID: projectID, RunID: runID, Paths: []string{"outputs"}}, true)
        if err != nil || transfer.ChangedFiles != want || transfer.Retried {
            t.Fatal(transfer, err)
        }
    }
    contents, err := os.ReadFile(filepath.Join(destination, "outputs", "a-result.txt"))
    if err != nil || string(contents) != "native-linux-scope" || resolutions != 1 {
        t.Fatal(string(contents), resolutions, err)
    }
    var master *commandConnection
    for _, entry := range scope.entries {
        master = entry.master
    }
    if master == nil {
        t.Fatal("missing private master")
    }
    // The remote marker establishes that the native mux read is in flight.
    ready := filepath.Join(root, "read-started")
    type probeResult struct {
        out []byte
        err error
    }
    recovered := make(chan probeResult, 1)
    go func() {
        out, err := client.executeRead(ctx, target, ": >"+Quote(ready)+"; sleep 0.5; uname -s", nil, true)
        recovered <- probeResult{out, err}
    }()
    tick := time.NewTicker(10 * time.Millisecond)
    defer tick.Stop()
    for {
        if _, err := client.Execute(ctx, target, "test -f "+Quote(ready), nil, true); err == nil {
            break
        }
        select {
        case <-ctx.Done():
            t.Fatal(ctx.Err())
        case <-tick.C:
        }
    }
    if err := master.process.Process.Kill(); err != nil {
        t.Fatal(err)
    }
    select {
    case result := <-recovered:
        if result.err != nil || string(result.out) != "Linux\n" || resolutions != 2 {
            t.Fatal("failed to recover native mux read", string(result.out), resolutions, result.err)
        }
    case <-ctx.Done():
        t.Fatal(ctx.Err())
    }
    // Keep native rsync's temporary-file and quick-check behavior. A completed
    // small file plus an unpublished large file establishes transfer readiness;
    // openrsync may buffer its temporary contents while its file size is zero.
    recoveryDestination := t.TempDir()
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Fatal(err)
    }
    slow := filepath.Join(t.TempDir(), "rsync")
    if err := os.WriteFile(slow, []byte("#!/bin/sh\nexec "+Quote(binary)+" --bwlimit=128 \"$@\"\n"), 0700); err != nil {
        t.Fatal(err)
    }
    sender.Binary = slow
    for _, entry := range scope.entries {
        master = entry.master
    }
    type pullResult struct {
        transfer Transfer
        err      error
    }
    pulled := make(chan pullResult, 1)
    go func() {
        result, err := sender.Pull(ctx, target, runner, recoveryDestination, PullInput{Root: root, ProjectID: projectID, RunID: runID, Paths: []string{"outputs"}}, true)
        pulled <- pullResult{result, err}
    }()
    small := filepath.Join(recoveryDestination, "outputs", "a-result.txt")
    large := filepath.Join(recoveryDestination, "outputs", "z-payload.bin")
    var before os.FileInfo
    for {
        before, _ = os.Stat(small)
        _, published := os.Stat(large)
        entries, _ := os.ReadDir(filepath.Dir(large))
        temporary := false
        for _, entry := range entries {
            if strings.HasPrefix(entry.Name(), ".z-payload.bin.") {
                temporary = true
            }
        }
        if before != nil && os.IsNotExist(published) && temporary {
            break
        }
        select {
        case result := <-pulled:
            t.Fatal("pull ended before fault injection", result)
        case <-ctx.Done():
            t.Fatal(ctx.Err())
        case <-tick.C:
        }
    }
    if err := master.process.Process.Kill(); err != nil {
        t.Fatal(err)
    }
    select {
    case result := <-pulled:
        if result.err != nil || !result.transfer.Retried || result.transfer.ChangedFiles != 1 || resolutions != 3 {
            t.Fatal("native pull did not recover once", result, resolutions)
        }
    case <-ctx.Done():
        t.Fatal(ctx.Err())
    }
    after, err := os.Stat(small)
    if err != nil || !os.SameFile(before, after) {
        t.Fatal("replaced already completed file", err)
    }
    data, err := os.ReadFile(large)
    if err != nil || !bytes.Equal(data, payload) {
        t.Fatal("recovered native payload differs", err)
    }
    sender.Binary = binary
    transfer, err := sender.Pull(ctx, target, runner, recoveryDestination, PullInput{Root: root, ProjectID: projectID, RunID: runID, Paths: []string{"outputs"}}, true)
    if err != nil || transfer.Retried || transfer.ChangedFiles != 0 {
        t.Fatal(transfer, err)
    }
    report := trace.Snapshot()
    if report.Reconnects != 2 || report.MastersEstablished != 3 || report.ReadRetries != 2 {
        t.Fatal(report)
    }
    session, err := client.prepare(ctx, target, true)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.Remove(privateSocket(session)); err != nil {
        t.Fatal(err)
    }
    sentinel := filepath.Join(root, "must-not-execute")
    if _, err := client.Execute(ctx, target, "touch "+Quote(sentinel), nil, true); err == nil {
        t.Fatal("native SSH silently fell back from the lost private socket")
    }
    direct := &Client{Binary: client.Binary}
    resolved := config.Target{Destination: dest, Args: []string{"-F", cfg}}
    if _, err := direct.Execute(ctx, resolved, "test ! -e "+Quote(sentinel), nil, true); err != nil || resolutions != 3 {
        t.Fatal("mutation ran after losing the private socket", resolutions, err)
    }
}
