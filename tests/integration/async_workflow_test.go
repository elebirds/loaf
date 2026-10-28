package integration

import (
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

func asyncCLIData[T any](t *testing.T, project string, args ...string) T {
    t.Helper()
    code, out := cli(t, project, args...)
    var result struct {
        Data T `json:"data"`
    }
    if code != 0 || json.Unmarshal(out, &result) != nil {
        t.Fatalf("%v: %d %s", args, code, out)
    }
    return result.Data
}

// The packaged client exits before the workload finishes. No Bridge runs until
// the fact exists, then a new client process recovers and delivers it once.
func TestPackagedDirectRunEventReplayAndDelivery(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    sourceFile(t, project, "loaf.toml", "[outputs]\npaths=['outputs']\n[run.env]\nTEST_TOKEN='event-private-value'\n")
    var mu sync.Mutex
    var bodies []string
    destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        body, err := io.ReadAll(io.LimitReader(r.Body, 128<<10))
        if err != nil {
            http.Error(w, "read", 500)
            return
        }
        mu.Lock()
        bodies = append(bodies, string(body))
        mu.Unlock()
        w.WriteHeader(http.StatusAccepted)
    }))
    defer destination.Close()
    asyncCLIData[client.DestinationRecord](t, project, "notify", "destination", "add", "results", "--kind", "webhook", "--endpoint", destination.URL, "--json")
    accepted := runCLI(t, project, "run", "--notify", "results", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs; while [ ! -f /tmp/loaf-event-release ]; do sleep .03; done; test "$TEST_TOKEN" = event-private-value; printf done >outputs/result.txt`)
    if accepted.Submission != "submitted" {
        t.Fatal("Run was not accepted", accepted)
    }
    mu.Lock()
    before := len(bodies)
    mu.Unlock()
    if before != 0 {
        t.Fatal("delivery unexpectedly required the submission call", before)
    }
    f.shell(`touch /tmp/loaf-event-release`)
    wait(t, 15*time.Second, func() bool {
        code, out := cli(t, project, "status", accepted.ID, "--json")
        var response struct {
            Data execution.View `json:"data"`
        }
        return code == 0 && json.Unmarshal(out, &response) == nil && response.Data.Status == "succeeded"
    })
    page := asyncCLIData[event.Page](t, project, "events", "list", "--source", "project", "--json")
    terminal := ""
    for _, e := range page.Events {
        if e.Subject.ID == accepted.ID && e.Type == "run.succeeded" {
            terminal = e.ID
        }
    }
    if terminal == "" {
        t.Fatal("missing durable terminal event", page)
    }
    serialized, _ := json.Marshal(page)
    if strings.Contains(string(serialized), "event-private-value") || strings.Contains(string(serialized), `"env"`) {
        t.Fatal("private execution leaked through event", string(serialized))
    }
    asyncCLIData[client.NotifyServeResult](t, project, "notify", "serve", "--once", "--json")
    deliveries := asyncCLIData[[]client.Delivery](t, project, "notify", "deliveries", "--json")
    delivered := false
    for _, d := range deliveries {
        if d.EventID == terminal {
            delivered = d.Receipt.Accepted()
            if d.Receipt.State == "processed" {
                t.Fatal("HTTP acceptance was reported as Agent processing", d)
            }
        }
    }
    mu.Lock()
    count := len(bodies)
    mu.Unlock()
    if !delivered || count == 0 {
        t.Fatal("event did not reach the explicit destination", deliveries, count)
    }
    asyncCLIData[client.NotifyServeResult](t, project, "notify", "serve", "--once", "--json")
    mu.Lock()
    after := len(bodies)
    mu.Unlock()
    if after != count {
        t.Fatal("successfully delivered event was replayed", count, after)
    }
    if code, out := cli(t, project, "clean", accepted.ID, "--yes", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    retained := asyncCLIData[event.Page](t, project, "events", "list", "--source", "project", "--json")
    found := false
    for _, e := range retained.Events {
        found = found || e.ID == terminal
    }
    if !found {
        t.Fatal("payload cleanup deleted the recoverable completion event", retained)
    }
}

// Two project identities and aliases share one remote ledger. Later source sync
// cannot rewrite queued input, and canceling a third request cannot stop the first.
func TestPackagedFIFOSharedTargetFrozenInputAndCancellation(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    first, second := stopProject(t, f), stopProject(t, f)
    for _, project := range []string{first, second} {
        sourceFile(t, project, "loaf.toml", "[outputs]\npaths=['outputs']\n")
    }
    if code, out := cli(t, second, "machine", "add", "same-host", "--ssh", "ssh -F "+ssh.Quote(f.config)+" fixture", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    if code, out := cli(t, second, "machine", "use", "same-host", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    var deliveredMu sync.Mutex
    messages := map[string]agentdelivery.Message{}
    receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var m agentdelivery.Message
        if json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&m) != nil {
            http.Error(w, "invalid", 400)
            return
        }
        deliveredMu.Lock()
        if _, exists := messages[m.ID]; exists {
            t.Error("duplicate packaged delivery", m.ID)
        }
        messages[m.ID] = m
        deliveredMu.Unlock()
        w.WriteHeader(202)
    }))
    defer receiver.Close()
    for _, project := range []string{first, second} {
        asyncCLIData[client.DestinationRecord](t, project, "notify", "destination", "add", "results", "--kind", "webhook", "--endpoint", receiver.URL, "--json")
    }
    enabled := asyncCLIData[queue.Result](t, first, "queue", "enable", "--slots", "1", "--json")
    if enabled.Status == nil || enabled.Status.Slots != 1 {
        t.Fatal("missing shared queue status", enabled)
    }
    a := asyncCLIData[client.QueueSubmitResult](t, first, "queue", "submit", "--notify", "results", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs; printf A >outputs/result.txt; while [ ! -f /tmp/loaf-queue-release ]; do sleep .03; done`)
    if a.Request == nil || a.Request.RunID == "" {
        t.Fatal("queue lost frozen Run identity", a)
    }
    sourceFile(t, second, "input.txt", "before")
    b := asyncCLIData[client.QueueSubmitResult](t, second, "queue", "submit", "--notify", "results", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs; cat input.txt >outputs/result.txt`)
    if b.Request == nil || b.Request.RunID == "" || b.Request.TargetID != a.Request.TargetID || b.Request.Sequence <= a.Request.Sequence {
        t.Fatal("projects/aliases did not share FIFO admission", a, b)
    }
    waiting := asyncCLIData[queue.Result](t, second, "queue", "show", b.Request.ID, "--json")
    if waiting.Request == nil || waiting.Request.State != "queued" {
        t.Fatal("second request bypassed occupied slot", waiting)
    }
    sourceFile(t, second, "input.txt", "after")
    if code, out := cli(t, second, "sync", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    c := asyncCLIData[client.QueueSubmitResult](t, second, "queue", "submit", "--notify", "results", "--json", "--", "/bin/sh", "-c", `touch /tmp/loaf-canceled-was-started`)
    if c.Request == nil {
        t.Fatal("missing cancelable request", c)
    }
    canceled := asyncCLIData[queue.Result](t, second, "queue", "cancel", c.Request.ID, "--json")
    if canceled.Request == nil || canceled.Request.State != "canceled" {
        t.Fatal("waiting request not canceled", canceled)
    }
    type cleanView struct {
        DryRun     bool         `json:"dry_run"`
        RequestIDs []string     `json:"request_ids"`
        Result     queue.Result `json:"result"`
    }
    preview := asyncCLIData[cleanView](t, second, "queue", "clean", c.Request.ID, "--dry-run", "--json")
    cleaned := asyncCLIData[cleanView](t, second, "queue", "clean", c.Request.ID, "--yes", "--json")
    if !preview.DryRun || cleaned.DryRun || len(cleaned.RequestIDs) != 1 || cleaned.RequestIDs[0] != c.Request.ID {
        t.Fatal("cleanup preview/execution lost explicit selection", preview, cleaned)
    }
    f.shell(`touch /tmp/loaf-queue-release`)
    wait(t, 20*time.Second, func() bool {
        code, out := cli(t, second, "queue", "show", b.Request.ID, "--json")
        if code != 0 {
            return false
        }
        var queued struct {
            Data queue.Result `json:"data"`
        }
        if json.Unmarshal(out, &queued) != nil || queued.Data.Request == nil {
            return false
        }
        code, out = cli(t, second, "status", b.Request.RunID, "--json")
        var response struct {
            Data execution.View `json:"data"`
        }
        return code == 0 && json.Unmarshal(out, &response) == nil && response.Data.Status == "succeeded"
    })
    pulled := pullCLI(t, second, 0, b.Request.RunID, "--json")
    data, err := os.ReadFile(filepath.Join(pulled.Destination, "outputs", "result.txt"))
    if err != nil || string(data) != "before" {
        t.Fatal("queued input changed after sync", string(data), err)
    }
    f.shell(`test ! -e /tmp/loaf-canceled-was-started`)
    for _, project := range []string{first, second} {
        served := asyncCLIData[client.NotifyServeResult](t, project, "notify", "serve", "--once", "--json")
        if served.Accepted == 0 || served.Unconfirmed != 0 {
            t.Fatal("queue notifications not wired", served)
        }
        subscriptions := asyncCLIData[client.SubscriptionPage](t, project, "notify", "subscriptions", "--json").Subscriptions
        for _, sub := range subscriptions {
            if len(sub.Sources) != 2 {
                t.Fatal("not one multi-source intent", sub)
            }
        }
        replay := asyncCLIData[client.NotifyServeResult](t, project, "notify", "serve", "--once", "--json")
        if replay.Attempted != 0 {
            t.Fatal("queue fact resent", replay)
        }
    }
    deliveredMu.Lock()
    defer deliveredMu.Unlock()
    succeeded, canceledEvent := 0, 0
    for _, m := range messages {
        if m.Subject == "run.succeeded" {
            succeeded++
        }
        if m.Subject == "queue.canceled" {
            canceledEvent++
        }
    }
    if succeeded != 2 || canceledEvent != 1 {
        t.Fatal("missing terminal/cancel delivery", messages)
    }
}
