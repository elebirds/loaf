package ssh_test

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

func TestNativeResumableLogConnection(t *testing.T) {
    for _, unconfirmed := range []bool{false, true} {
        name := "confirmed"
        if unconfirmed {
            name = "unconfirmed"
        }
        t.Run(name, func(t *testing.T) { nativeResumableLogConnection(t, unconfirmed) })
    }
}

func nativeResumableLogConnection(t *testing.T, unconfirmed bool) {
    runner := os.Getenv("LOAF_NATIVE_SCOPE_RUNNER")
    if runner == "" {
        t.Skip("run bash scripts/integration.sh")
    }
    cfg, dest := os.Getenv("LOAF_NATIVE_SCOPE_CONFIG"), os.Getenv("LOAF_NATIVE_SCOPE_DEST")
    if cfg == "" {
        cfg = "/tmp/linux-client/config"
    }
    if dest == "" {
        dest = "researcher@self"
    }
    root := os.Getenv("LOAF_NATIVE_SCOPE_ROOT")
    if root == "" {
        root = filepath.Join(t.TempDir(), "logs")
    } else {
        root += "-logs"
        if unconfirmed {
            root += "-unconfirmed"
        }
    }
    ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
    defer cancel()
    scope := ssh.NewConnections()
    defer scope.Close()
    resolved := config.Target{Destination: dest, Args: []string{"-F", cfg}}
    resolutions := 0
    client := &ssh.Client{Binary: "ssh", Connections: scope, Resolve: func(_ context.Context, bound config.Target) (config.Target, string, error) {
        if bound.Cloud.Ref.ID != "same-instance" {
            t.Error("changed binding")
        }
        resolutions++
        return resolved, "", nil
    }}
    target := config.Target{RemoteBase: "~/projects", Cloud: &cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "fixture", ID: "same-instance"}, Settings: cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Env: "LOAF_FIXTURE_TOKEN"}}}}
    source := t.TempDir()
    if err := os.WriteFile(filepath.Join(source, "source.txt"), []byte("source"), 0600); err != nil {
        t.Fatal(err)
    }
    const projectID = "p_1123456789abcdef01234567"
    const runID = "r_1123456789abcdef01234567"
    sender := ssh.Rsync{Client: client, Binary: "rsync"}
    if _, err := sender.Sync(ctx, target, runner, source, ssh.SyncInput{Root: root, ProjectID: projectID, RunID: runID, Mode: "mirror", Paths: []string{"source.txt"}}, true); err != nil {
        t.Fatal(err)
    }
    release := filepath.Join(root, "release-log")
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "launch", Root: root, ProjectID: projectID, RunID: runID, Args: []string{"/bin/sh", "-c", "printf 'first\\n'; i=0; while [ ! -f " + ssh.Quote(release) + " ]; do i=$((i+1)); test $i -lt 400 || exit 90; sleep .05; done; printf 'second 中文\\n'"}}
    record, err := client.Control(ctx, target, runner, req, true)
    if err == nil && unconfirmed {
        // Lose one acceptance response so recovery is exercised even when the
        // fixture's supervisor starts within the normal confirmation budget.
        record, err = nil, &ssh.RemoteError{Code: "SUBMISSION_UNKNOWN"}
    }
    if err != nil {
        var remote *ssh.RemoteError
        if !errors.As(err, &remote) || remote.Code != "SUBMISSION_UNKNOWN" {
            t.Fatal(err)
        }
    }
    // A slow supervisor can outlive launch's finite confirmation budget. Logs
    // need a live owner; observe this same ID until it is ready, never relaunch.
    query := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "status", Root: root, ProjectID: projectID, RunID: runID}
    tick := time.NewTicker(100 * time.Millisecond)
    defer tick.Stop()
    for record == nil || record.SupervisorPID == 0 {
        select {
        case <-ctx.Done():
            t.Fatal("supervisor acceptance was not confirmed", ctx.Err())
        case <-tick.C:
        }
        record, err = client.Control(ctx, target, runner, query, true)
        if err != nil {
            t.Fatal("observe original launch", err)
        }
        if record != nil && (wire.Terminal(record.Status) || record.Status == "unknown") {
            t.Fatalf("log fixture did not reach a live supervisor: status=%s phase=%s reason=%s", record.Status, record.Phase, record.Reason)
        }
    }
    ref := task.RunRef{ID: runID, ProjectID: projectID, Target: "fixture", Connection: target, RemoteDir: root, Runner: runner, Version: wire.Version, CreatedAt: time.Now(), Submission: "submitted"}
    service := execution.Service{Remote: client, Runner: func(context.Context, config.Target, string, bool) (string, error) { return runner, nil }}
    var text strings.Builder
    ends := 0
    interrupted := false
    firstCursor := ""
    last, err := service.FollowResumable(ctx, ref, 0, 100, true, "", func(v execution.View, kind string) error {
        text.WriteString(v.Log.Text)
        if kind == "end" {
            ends++
        }
        if !interrupted && strings.Contains(v.Log.Text, "first\n") {
            interrupted = true
            firstCursor = v.Log.Cursor
            if err := ssh.InterruptPrivateMasterForTest(client); err != nil {
                return err
            }
            direct := &ssh.Client{Binary: "ssh"}
            _, err := direct.Execute(ctx, resolved, ": >"+ssh.Quote(release), nil, true)
            return err
        }
        return nil
    })
    if err != nil || !interrupted || ends != 1 || text.String() != "first\nsecond 中文\n" || last.Status != "succeeded" || resolutions != 2 {
        t.Fatal(last, err, text.String(), ends, resolutions)
    }
    // Rewriting a completed log in place must not turn a saved cursor into a
    // silent tail restart, even when the rewritten file is longer.
    direct := &ssh.Client{Binary: "ssh"}
    _, err = direct.Execute(ctx, resolved, "printf 'replacement content longer than the original log' >"+ssh.Quote(filepath.Join(root, "runs", runID, "output.log")), nil, true)
    if err != nil {
        t.Fatal(err)
    }
    _, err = service.FollowResumable(ctx, ref, 0, 100, true, firstCursor, nil)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "LOG_CHANGED" || resolutions != 2 {
        t.Fatal(err, resolutions)
    }
}
