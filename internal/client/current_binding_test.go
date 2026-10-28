package client

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/state/notifications"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

type bindingProbe struct {
    notificationSender
    calls     int
    available bool
}

func (p *bindingProbe) Probe(_ context.Context, d agentdelivery.Destination) (agentdelivery.Capabilities, error) {
    p.calls++
    p.destination = d
    return agentdelivery.Capabilities{Available: p.available, ExistingSession: p.available}, nil
}

func TestCurrentBindingVerifiesPersistsAndIsIdempotent(t *testing.T) {
    state := local.New(t.TempDir())
    sender := &bindingProbe{available: true}
    destination := agentdelivery.Destination{Kind: "codex-desktop", Endpoint: "/private/current.sock", SessionID: "exact_current_thread"}
    _, notify := NewEventServices(EventDependencies{State: state, Sender: sender, CurrentCodex: destination})
    binder := notify.(CurrentBindingService)
    result, err := binder.BindCurrent(context.Background(), BindCurrentInput{Name: "here"})
    if err != nil || result.Destination.SessionID != destination.SessionID || result.VerifiedAt.IsZero() || sender.calls != 1 {
        t.Fatal(result, err, sender.calls)
    }
    saved, err := (notifications.Store{Path: state.Path}).Destinations(context.Background())
    if err != nil || len(saved) != 1 || saved[0].Address != savedAddress(destination) {
        t.Fatal(saved, err)
    }
    again, err := binder.BindCurrent(context.Background(), BindCurrentInput{Name: "here"})
    if err != nil || again.Destination.CreatedAt != result.Destination.CreatedAt || sender.calls != 2 {
        t.Fatal(again, err, sender.calls)
    }
    encoded, _ := json.Marshal(result)
    if strings.Contains(string(encoded), "/private/current.sock") || strings.Contains(string(encoded), "endpoint") {
        t.Fatal("private connection leaked", string(encoded))
    }
}

func TestCurrentBindingFailureDoesNotWriteState(t *testing.T) {
    for _, missing := range []bool{false, true} {
        state := local.New(t.TempDir())
        sender := &bindingProbe{}
        destination := agentdelivery.Destination{Kind: "codex-desktop", Endpoint: "/private/current.sock", SessionID: "thread_1"}
        if missing {
            destination.SessionID = ""
        }
        _, notify := NewEventServices(EventDependencies{State: state, Sender: sender, CurrentCodex: destination})
        _, err := notify.(CurrentBindingService).BindCurrent(context.Background(), BindCurrentInput{Name: "here"})
        var failure *fault.Error
        if !errors.As(err, &failure) || failure.Code != "CURRENT_SESSION_UNAVAILABLE" {
            t.Fatal(err)
        }
        if _, err := os.Stat(state.Path); !os.IsNotExist(err) {
            t.Fatal("failed verification mutated local state", err)
        }
        if missing && sender.calls != 0 {
            t.Fatal("missing exact context was guessed", sender.calls)
        }
    }
}

func TestCurrentBindingCannotSilentlyReplaceAnotherSession(t *testing.T) {
    state := local.New(t.TempDir())
    sender := &bindingProbe{available: true}
    destination := agentdelivery.Destination{Kind: "codex-desktop", Endpoint: "/private/current.sock", SessionID: "current_thread"}
    _, notify := NewEventServices(EventDependencies{State: state, Sender: sender, CurrentCodex: destination})
    old := destination
    old.SessionID = "previous_thread"
    if _, err := notify.PutDestination(context.Background(), DestinationInput{Name: "here", Destination: old}); err != nil {
        t.Fatal(err)
    }
    binder := notify.(CurrentBindingService)
    _, err := binder.BindCurrent(context.Background(), BindCurrentInput{Name: "here"})
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "DESTINATION_EXISTS" {
        t.Fatal(err)
    }
    saved, _ := (notifications.Store{Path: state.Path}).Destinations(context.Background())
    if len(saved) != 1 || saved[0].Address != savedAddress(old) {
        t.Fatal("old session redirected", saved)
    }
    if _, err := binder.BindCurrent(context.Background(), BindCurrentInput{Name: "here", Replace: true}); err != nil {
        t.Fatal(err)
    }
}
