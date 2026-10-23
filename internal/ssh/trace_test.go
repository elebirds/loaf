package ssh

import (
    "context"
    "encoding/json"
    "strings"
    "sync"
    "testing"

    "github.com/elebirds/loaf/internal/fault"
)

func TestTraceCountsPrivateMastersWithoutLeakingInputs(t *testing.T) {
    client, target, _, _ := scopedFixture(t)
    trace := &Trace{}
    trace.Enable()
    client.Trace = trace
    for range 3 {
        if _, err := client.Execute(context.Background(), target, "secret-command-must-not-be-in-trace", strings.NewReader("secret-input"), true); err != nil {
            t.Fatal(err)
        }
    }
    report := trace.Snapshot()
    if report.ConnectionResolutions != 1 || report.MastersEstablished != 1 || report.Reconnects != 0 || report.ReadRetries != 0 {
        t.Fatal(report)
    }
    trace.stage("observe")(fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "secret-password", "Token": "secret-token"}))
    data, err := json.Marshal(trace.Snapshot())
    if err != nil || strings.Contains(string(data), "secret-") || strings.Contains(string(data), "original-instance") || strings.Contains(string(data), "root@") {
        t.Fatal(string(data), err)
    }
    trace.Reset()
    if trace.Snapshot() != nil {
        t.Fatal("trace leaked across invocations")
    }
}

func TestTraceIsConcurrentBoundedAndKeepsRetryCounts(t *testing.T) {
    trace := &Trace{}
    trace.Enable()
    var wg sync.WaitGroup
    for range 8 {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for range 80 {
                trace.stage("observe")(nil)
                trace.retried(fault.New("SSH_FAILED", "ssh_failed", nil))
            }
        }()
    }
    wg.Wait()
    report := trace.Snapshot()
    if len(report.Stages) != 512 || !report.Truncated || report.ReadRetries != 640 {
        t.Fatal(report)
    }
    report.Stages[0].Stage = "mutated"
    if trace.Snapshot().Stages[0].Stage == "mutated" {
        t.Fatal("snapshot aliases trace")
    }
}
