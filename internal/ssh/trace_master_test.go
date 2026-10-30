//go:build !windows

package ssh

import (
    "context"
    "encoding/json"
    "github.com/elebirds/loaf/internal/fault"
    "strings"
    "testing"
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
