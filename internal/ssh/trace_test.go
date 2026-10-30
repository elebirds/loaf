package ssh

import (
    "sync"
    "testing"

    "github.com/elebirds/loaf/internal/fault"
)

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
