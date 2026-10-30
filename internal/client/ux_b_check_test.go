package client

import (
    "context"
    "errors"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
)

type uxBChecker func(context.Context, config.Target, string, bool) (ssh.Report, error)

func (f uxBChecker) Check(ctx context.Context, target config.Target, directory string, batch bool) (ssh.Report, error) {
    return f(ctx, target, directory, batch)
}

func uxBMachines(t *testing.T, checker Checker) *Machines {
    t.Helper()
    root := t.TempDir()
    store := config.New(filepath.Join(root, "config"), "", "")
    settings := config.Global{Targets: map[string]config.Target{}}
    for _, name := range []string{"one", "two", "three"} {
        settings.Targets[name] = config.Target{Destination: name, RemoteBase: "~/projects"}
    }
    return &Machines{Settings: settings, Config: store, State: local.New(store.StateDir), Checker: checker, Trace: &ssh.Trace{}, CWD: root}
}

func TestUXBCheckDeadlinePreservesOriginalFaultAndReport(t *testing.T) {
    original := fault.At(fault.New("SSH_FAILED", "ssh_failed", nil), "preflight")
    machines := uxBMachines(t, uxBChecker(func(ctx context.Context, _ config.Target, _ string, _ bool) (ssh.Report, error) {
        <-ctx.Done()
        return ssh.Report{OS: "Linux", Arch: "aarch64"}, original
    }))
    name := "one"
    result, err := machines.Check(context.Background(), CheckInput{Target: &name, Timeout: time.Millisecond})
    if !errors.Is(err, original) || !errors.Is(err, context.DeadlineExceeded) || ssh.FailureKind(err) != "observation_timeout" || fault.Stage(err) != "preflight" {
        t.Fatal(result, err, ssh.FailureKind(err), fault.Stage(err))
    }
    if result.Single == nil || result.Single.Report.OS != "Linux" || result.Single.Steps["platform"] != "verified" || result.Single.FailureKind != "observation_timeout" {
        t.Fatal(result.Single)
    }
}

func TestUXBCheckCancellationKeepsEverySelectedRow(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    calls := 0
    machines := uxBMachines(t, uxBChecker(func(_ context.Context, _ config.Target, _ string, batch bool) (ssh.Report, error) {
        if batch {
            t.Fatal("interactive checks must remain serial")
        }
        calls++
        cancel()
        return ssh.Report{}, fault.New("SSH_FAILED", "ssh_failed", nil)
    }))
    result, err := machines.Check(ctx, CheckInput{Targets: []string{"one", "two", "three"}, Timeout: 20 * time.Second})
    var problem *fault.Error
    if !errors.As(err, &problem) || problem.Code != "CHECK_PARTIAL" || calls != 1 || result.Batch == nil {
        t.Fatal(result, err, calls)
    }
    batch := result.Batch
    if batch.Selected != 3 || batch.Completed != 0 || batch.Failed != 1 || batch.Unexamined != 2 || len(batch.Checks) != 3 {
        t.Fatal(batch)
    }
    for i, name := range []string{"one", "two", "three"} {
        if batch.Checks[i].Target != name {
            t.Fatal(batch.Checks)
        }
    }
    if ssh.FailureKind(batch.Checks[0].Error) != "canceled" {
        t.Fatal(batch.Checks[0])
    }
}

func TestUXBCheckRejectsInvalidSelectionBeforeAnyObservation(t *testing.T) {
    calls := 0
    machines := uxBMachines(t, uxBChecker(func(context.Context, config.Target, string, bool) (ssh.Report, error) {
        calls++
        return ssh.Report{}, nil
    }))
    name := "one"
    for _, input := range []CheckInput{
        {Targets: []string{}, Timeout: 20 * time.Second},
        {Targets: make([]string, 33), Timeout: 20 * time.Second},
        {Targets: []string{"one", "one"}, Timeout: 20 * time.Second},
        {Target: &name, Targets: []string{"one"}, Timeout: 20 * time.Second},
        {Target: &name, Timeout: 0},
    } {
        _, err := machines.Check(context.Background(), input)
        var problem *fault.Error
        if !errors.As(err, &problem) || problem.Code != "BAD_CONFIG" || calls != 0 {
            t.Fatal(input, err, calls)
        }
    }
}
