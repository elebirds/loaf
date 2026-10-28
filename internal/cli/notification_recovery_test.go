package cli

import (
    "context"
    "encoding/json"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/eventlog"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

type cliUnknownSender struct{ calls int }

func (*cliUnknownSender) Probe(context.Context, agentdelivery.Destination) (agentdelivery.Capabilities, error) {
    return agentdelivery.Capabilities{Available: true}, nil
}
func (s *cliUnknownSender) Deliver(_ context.Context, _ agentdelivery.Destination, m agentdelivery.Message) (agentdelivery.Receipt, error) {
    s.calls++
    return agentdelivery.Receipt{MessageID: m.ID, State: "unconfirmed", ReasonCode: "TEST_LOST_REPLY"}, agentdelivery.ErrUnconfirmed
}
func TestNotificationRecoveryCommandsUseRealStateAndExplicitConsent(t *testing.T) {
    o := fixture(t)
    sender := &cliUnknownSender{}
    events, notify := client.NewEventServices(client.EventDependencies{State: o.State, CWD: o.CWD, Sender: sender})
    o.Notify = notify
    o.Events = events
    ctx := context.Background()
    if _, err := notify.PutDestination(ctx, client.DestinationInput{Name: "test", Destination: agentdelivery.Destination{Kind: "webhook", Endpoint: "http://127.0.0.1:12345/private-endpoint"}}); err != nil {
        t.Fatal(err)
    }
    sub, err := notify.Subscribe(ctx, client.SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_test"}, Source: "resources", Destinations: []string{"test"}, Replay: true})
    if err != nil {
        t.Fatal(err)
    }
    database, err := db.Open(ctx, o.State.Path, true)
    if err != nil {
        t.Fatal(err)
    }
    tx, _ := database.Begin()
    _, err = eventlog.Append(ctx, tx, "test:ready", event.Envelope{Type: "resource.ready", Subject: sub.Subject, Correlation: event.Correlation{CloudRequestID: "c_test"}})
    if err != nil {
        t.Fatal(err)
    }
    tx.Commit()
    database.Close()
    code, out, stderr := run(t, o, "notify", "serve", "--once", "--json")
    if code != 3 || stderr != "" || !strings.Contains(out, "DELIVERY_UNCONFIRMED") {
        t.Fatal(code, out, stderr)
    }
    rows, _ := notify.Deliveries(ctx, 10)
    id := rows[0].ID
    for _, args := range [][]string{{"notify", "delivery", "resend", id, "--json"}, {"notify", "delivery", "resend", id, "--allow-duplicate", "--json"}} {
        code, out, stderr = run(t, o, args...)
        if code != 2 || sender.calls != 1 {
            t.Fatal("consent bypass", code, out, stderr, sender.calls)
        }
    }
    code, out, stderr = run(t, o, "notify", "delivery", "show", id, "--json")
    if code != 0 || strings.Contains(out, "private-endpoint") || !strings.Contains(out, `"attempts":[`) {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "delivery", "resend", id, "--allow-duplicate", "--yes", "--json")
    if code != 0 || sender.calls != 1 {
        t.Fatal("resend command itself sent", code, out, stderr, sender.calls)
    }
    var response struct {
        Data client.Delivery `json:"data"`
    }
    json.Unmarshal([]byte(out), &response)
    child := response.Data.ID
    code, out, stderr = run(t, o, "notify", "delivery", "resend", id, "--allow-duplicate", "--yes", "--json")
    if code != 0 || !strings.Contains(out, child) {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "serve", "--once", "--json")
    if code != 3 || sender.calls != 2 {
        t.Fatal(code, out, stderr, sender.calls)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr = run(t, o, "--lang", lang, "notify", "delivery", "show", child)
        if code != 0 || out == "" || stderr != "" {
            t.Fatal(lang, code, out, stderr)
        }
    }
    code, out, stderr = run(t, o, "notify", "delivery", "resolve", child, "--as", "confirmed", "--json")
    if code != 0 || !strings.Contains(out, `"state":"unconfirmed"`) || !strings.Contains(out, `"kind":"confirmed"`) {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "destination", "remove", "test", "--json")
    if code != 1 || !strings.Contains(out, "DESTINATION_IN_USE") || !strings.Contains(out, sub.ID) {
        t.Fatal(code, out, stderr)
    }
    for _, op := range []string{"disable", "enable", "remove"} {
        code, out, stderr = run(t, o, "notify", "subscription", op, sub.ID, "--json")
        if code != 0 {
            t.Fatal(code, out, stderr)
        }
    }
    code, out, stderr = run(t, o, "notify", "destination", "remove", "test", "--json")
    if code != 0 {
        t.Fatal(code, out, stderr)
    }
    cutoff := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
    code, out, stderr = run(t, o, "notify", "prune", "--before", cutoff, "--json")
    if code != 2 {
        t.Fatal("prune without consent", code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "prune", "--before", cutoff, "--dry-run", "--json")
    if code != 0 || !strings.Contains(out, `"dry_run":true`) {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "prune", "--before", cutoff, "--yes", "--json")
    if code != 0 || !strings.Contains(out, `"dry_run":false`) {
        t.Fatal(code, out, stderr)
    }
}
func TestWebhookPolicyFlagsAndExperimentalBindingMetadata(t *testing.T) {
    o := fixture(t)
    _, o.Notify = client.NewEventServices(client.EventDependencies{State: o.State, CWD: o.CWD})
    for _, language := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", language, "help", "notify", "delivery", "resend", "--json")
        if code != 0 || !strings.Contains(out, "allow-duplicate") || stderr != "" {
            t.Fatal(code, out, stderr)
        }
    }
    code, out, stderr := run(t, o, "notify", "destination", "add", "remote", "--kind", "webhook", "--endpoint", "http://receiver.example/hook", "--json")
    if code != 2 {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "destination", "add", "remote", "--kind", "webhook", "--endpoint", "http://receiver.example/hook", "--allow-insecure-http", "--json")
    if code != 0 {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "destination", "add", "auth", "--kind", "webhook", "--endpoint", "https://receiver.example/hook", "--bearer-env", "HOOK_TOKEN", "--json")
    if code != 0 || strings.Contains(out, "receiver.example") {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "destination", "add", "desktop", "--kind", "codex-desktop", "--endpoint", "/private/ipc.sock", "--session", "exact_thread", "--json")
    if code != 0 || !strings.Contains(out, `"experimental":true`) {
        t.Fatal(code, out, stderr)
    }
}

func TestSubscriptionListPaginatesCurrentState(t *testing.T) {
    o := fixture(t)
    _, notify := client.NewEventServices(client.EventDependencies{State: o.State, CWD: o.CWD})
    o.Notify = notify
    ctx := context.Background()
    notify.PutDestination(ctx, client.DestinationInput{Name: "test", Destination: agentdelivery.Destination{Kind: "webhook", Endpoint: "http://127.0.0.1/hook"}})
    for i := 0; i < 2; i++ {
        if _, err := notify.Subscribe(ctx, client.SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_test"}, Source: "resources", Destinations: []string{"test"}, Replay: true}); err != nil {
            t.Fatal(err)
        }
    }
    code, out, stderr := run(t, o, "notify", "subscriptions", "--limit", "1", "--json")
    var page struct {
        Data client.SubscriptionPage `json:"data"`
    }
    if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &page) != nil || len(page.Data.Subscriptions) != 1 || !page.Data.HasMore || page.Data.NextAfter == "" {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "subscriptions", "--limit", "1", "--after", page.Data.NextAfter, "--json")
    if code != 0 || json.Unmarshal([]byte(out), &page) != nil || len(page.Data.Subscriptions) != 1 || page.Data.HasMore {
        t.Fatal(code, out, stderr)
    }
}
