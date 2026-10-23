package ssh

import (
    "errors"
    "sort"
    "sync"
    "time"

    "github.com/elebirds/loaf/internal/fault"
)

// Trace records only fixed operation names, timings, counts and stable codes.
// Commands, endpoints, request bodies, credentials and environments are excluded.
type Trace struct {
    mu      sync.Mutex
    enabled bool
    start   time.Time
    report  TraceReport
}

// TraceStage is one timed operation, possibly containing other stages.
type TraceStage struct {
    Stage         string `json:"stage"`
    StartedMillis int64  `json:"started_millis"`
    ElapsedMillis int64  `json:"elapsed_millis"`
    Outcome       string `json:"outcome"`
    ErrorCode     string `json:"error_code,omitempty"`
    Reason        string `json:"reason,omitempty"`
}

// TraceReport is a bounded, secret-free snapshot for one CLI invocation.
type TraceReport struct {
    TotalMillis           int64        `json:"total_millis"`
    ConnectionResolutions int          `json:"connection_resolutions"`
    MastersEstablished    int          `json:"masters_established"`
    Reconnects            int          `json:"reconnects"`
    ReadRetries           int          `json:"read_retries"`
    Stages                []TraceStage `json:"stages"`
    Truncated             bool         `json:"truncated"`
}

// Reset separates consecutive Execute calls without enabling collection.
func (t *Trace) Reset() {
    if t == nil {
        return
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    t.enabled = false
    t.report = TraceReport{}
}

// Enable begins a command's trace. A nil trace disables collection.
func (t *Trace) Enable() {
    if t == nil {
        return
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    t.enabled, t.start, t.report = true, time.Now(), TraceReport{Stages: []TraceStage{}}
}

// Snapshot returns independent values, safe while history queries are active.
func (t *Trace) Snapshot() *TraceReport {
    if t == nil {
        return nil
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    if !t.enabled {
        return nil
    }
    r := t.report
    r.TotalMillis = time.Since(t.start).Milliseconds()
    r.Stages = append([]TraceStage{}, r.Stages...)
    sort.SliceStable(r.Stages, func(i, j int) bool { return r.Stages[i].StartedMillis < r.Stages[j].StartedMillis })
    return &r
}

func (t *Trace) stage(stage string) func(error) {
    if t == nil {
        return func(error) {}
    }
    t.mu.Lock()
    active, origin := t.enabled, t.start
    t.mu.Unlock()
    if !active {
        return func(error) {}
    }
    start := time.Now()
    return func(err error) {
        event := TraceStage{Stage: stage, StartedMillis: start.Sub(origin).Milliseconds(), ElapsedMillis: time.Since(start).Milliseconds(), Outcome: "ok"}
        if err != nil {
            event.Outcome = "error"
            var f *fault.Error
            if errors.As(err, &f) {
                event.ErrorCode = f.Code
            } else {
                event.ErrorCode = "OPERATION_FAILED"
            }
        }
        t.mu.Lock()
        defer t.mu.Unlock()
        if len(t.report.Stages) >= 512 {
            t.report.Truncated = true
            return
        }
        t.report.Stages = append(t.report.Stages, event)
    }
}

func (t *Trace) resolved() {
    if t == nil {
        return
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.enabled {
        t.report.ConnectionResolutions++
    }
}
func (t *Trace) established(reconnect bool) {
    if t == nil {
        return
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.enabled {
        t.report.MastersEstablished++
        if reconnect {
            t.report.Reconnects++
        }
    }
}
func (t *Trace) retried(err error) {
    if t == nil {
        return
    }
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.enabled {
        t.report.ReadRetries++
        reason, code := "ssh_transport", "SSH_FAILED"
        var f *fault.Error
        if errors.As(err, &f) {
            code = f.Code
            if f.Code == "CLOUD_FAILED" {
                reason = "cloud_transport"
            }
        }
        var scoped *scopedTransportError
        if errors.As(err, &scoped) {
            select {
            case <-scoped.masterDone:
                reason = "private_master_exit"
            default:
            }
        }
        if len(t.report.Stages) < 512 {
            t.report.Stages = append(t.report.Stages, TraceStage{Stage: "read_retry", StartedMillis: time.Since(t.start).Milliseconds(), Outcome: "retry", ErrorCode: code, Reason: reason})
        } else {
            t.report.Truncated = true
        }
    }
}
