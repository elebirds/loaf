package client

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

type uxISetupSender struct {
    probes, sends int
    destination   agentdelivery.Destination
    caps          agentdelivery.Capabilities
    err           error
}

func (s *uxISetupSender) Probe(_ context.Context, d agentdelivery.Destination) (agentdelivery.Capabilities, error) {
    s.probes++
    s.destination = d
    return s.caps, s.err
}
func (s *uxISetupSender) Deliver(context.Context, agentdelivery.Destination, agentdelivery.Message) (agentdelivery.Receipt, error) {
    s.sends++
    return agentdelivery.Receipt{}, errors.New("unexpected send")
}

func TestUXICurrentPreviewIsReadOnlyExactAndSafe(t *testing.T) {
    state := local.New(t.TempDir())
    sender := &uxISetupSender{caps: agentdelivery.Capabilities{Available: true, ExistingSession: true, Experimental: true}}
    d := agentdelivery.Destination{Kind: "codex-desktop", SessionID: "exact_thread", Endpoint: "/private/SECRET-endpoint"}
    _, notify := NewEventServices(EventDependencies{State: state, CurrentCodex: d, Sender: sender})
    setup := notify.(NotificationSetupService)
    preview, err := setup.PreviewCurrentBinding(context.Background())
    if err != nil || preview.Destination.SessionID != "exact_thread" || sender.destination != d || sender.sends != 0 || preview.CheckedAt.IsZero() {
        t.Fatal(preview, err, sender)
    }
    if _, err := os.Stat(state.Path); !os.IsNotExist(err) {
        t.Fatal("preview wrote state", err)
    }
    data, _ := json.Marshal(preview)
    if strings.Contains(string(data), "SECRET") {
        t.Fatal(string(data))
    }
    sender.caps.Available = false
    sender.err = errors.New("SECRET-token in private transport error")
    preview, err = setup.PreviewCurrentBinding(context.Background())
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "FEATURE_UNAVAILABLE" || strings.Contains(err.Error(), "SECRET") || sender.sends != 0 {
        t.Fatal(preview, err)
    }
    if _, err := os.Stat(state.Path); !os.IsNotExist(err) {
        t.Fatal("failed preview wrote state", err)
    }
}

func TestUXIMissingCurrentContextDoesNotProbe(t *testing.T) {
    sender := &uxISetupSender{}
    _, notify := NewEventServices(EventDependencies{State: local.New(t.TempDir()), Sender: sender})
    _, err := notify.(NotificationSetupService).PreviewCurrentBinding(context.Background())
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "CURRENT_SESSION_UNAVAILABLE" || sender.probes != 0 || sender.sends != 0 {
        t.Fatal(err, sender)
    }
}

func TestUXISubscriptionRecipientInspectionUsesFrozenAddress(t *testing.T) {
    sender := &uxISetupSender{caps: agentdelivery.Capabilities{Available: true, ExistingSession: true}}
    _, _, notify := notificationFixture(t, sender)
    ctx := context.Background()
    sub, err := notify.Subscribe(ctx, SubscribeInput{Source: "resources", Subject: event.Subject{Kind: "resource", ID: "known_instance"}, Destinations: []string{"agent"}, Replay: true})
    if err != nil {
        t.Fatal(err)
    }
    if _, err = notify.PutDestination(ctx, DestinationInput{Name: "agent", Destination: agentdelivery.Destination{Kind: "codex", SessionID: "replacement_thread", Endpoint: "/private/new-secret"}, Replace: true}); err != nil {
        t.Fatal(err)
    }
    setup := notify.(NotificationSetupService)
    rows, err := setup.SubscriptionRecipients(ctx, sub.ID)
    if err != nil || len(rows) != 1 || rows[0].SessionID != "thread_1" {
        t.Fatal(rows, err)
    }
    preview, err := setup.ProbeDestination(ctx, "agent")
    if err != nil || preview.Destination.SessionID != "replacement_thread" || sender.destination.SessionID != "replacement_thread" || sender.sends != 0 {
        t.Fatal(preview, err, sender)
    }
    for _, status := range []string{"disabled", "removed"} {
        if _, err = notify.(NotificationControlService).ChangeSubscription(ctx, sub.ID, status); err != nil {
            t.Fatal(err)
        }
    }
    if err = notify.(NotificationControlService).RemoveDestination(ctx, "agent"); err != nil {
        t.Fatal(err)
    }
    rows, err = setup.SubscriptionRecipients(ctx, sub.ID)
    if err != nil || len(rows) != 1 || rows[0].SessionID != "thread_1" || sender.sends != 0 {
        t.Fatal(rows, err)
    }
    data, _ := json.Marshal(rows)
    if strings.Contains(string(data), "new-secret") {
        t.Fatal(string(data))
    }
}
