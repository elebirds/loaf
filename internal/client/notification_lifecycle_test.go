package client

import (
    "context"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

// The marker stands for a destination having received the message before the
// Bridge can commit its receipt. Killing this owned child exercises the actual
// process/recovery boundary instead of clearing an in-memory service.
type interruptedBridgeSender struct{ marker string }

func (s interruptedBridgeSender) Probe(context.Context, agentdelivery.Destination) (agentdelivery.Capabilities, error) {
    return agentdelivery.Capabilities{Available: true}, nil
}

func (s interruptedBridgeSender) Deliver(ctx context.Context, _ agentdelivery.Destination, m agentdelivery.Message) (agentdelivery.Receipt, error) {
    if err := os.WriteFile(s.marker, []byte(m.ID), 0600); err != nil {
        return agentdelivery.Receipt{}, err
    }
    <-ctx.Done()
    return agentdelivery.Receipt{MessageID: m.ID, State: "unconfirmed"}, ctx.Err()
}

func TestBridgeInterruptedProcessHelper(t *testing.T) {
    directory := os.Getenv("LOAF_TEST_BRIDGE_STATE")
    if directory == "" {
        return
    }
    _, notify := NewEventServices(EventDependencies{State: local.New(directory), Sender: interruptedBridgeSender{marker: os.Getenv("LOAF_TEST_BRIDGE_MARKER")}})
    if _, err := notify.Serve(context.Background(), NotifyServeInput{Once: true}, nil); err != nil {
        t.Fatal(err)
    }
}

func TestBridgeKilledAfterPossibleSendRecoversWithoutReplay(t *testing.T) {
    state, _, notify := notificationFixture(t, &notificationSender{})
    ctx := context.Background()
    if _, err := notify.Subscribe(ctx, SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}, Source: "resources", Destinations: []string{"agent"}, Replay: true}); err != nil {
        t.Fatal(err)
    }
    appendResourceEvent(t, state)
    marker := filepath.Join(t.TempDir(), "destination-received")
    child := exec.Command(os.Args[0], "-test.run=^TestBridgeInterruptedProcessHelper$")
    child.Env = append(os.Environ(), "LOAF_TEST_BRIDGE_STATE="+state.Dir, "LOAF_TEST_BRIDGE_MARKER="+marker)
    if err := child.Start(); err != nil {
        t.Fatal(err)
    }
    reaped := false
    t.Cleanup(func() {
        if !reaped {
            _ = child.Process.Kill()
            _ = child.Wait()
        }
    })
    deadline := time.NewTimer(15 * time.Second)
    defer deadline.Stop()
    ticker := time.NewTicker(10 * time.Millisecond)
    defer ticker.Stop()
    var id string
    for id == "" {
        if data, err := os.ReadFile(marker); err == nil {
            id = string(data)
        }
        if id != "" {
            break
        }
        select {
        case <-deadline.C:
            t.Fatal("owned Bridge did not reach the possible-send boundary")
        case <-ticker.C:
        }
    }
    if err := child.Process.Kill(); err != nil {
        t.Fatal(err)
    }
    _ = child.Wait()
    reaped = true

    sender := &notificationSender{}
    events, restarted := NewEventServices(EventDependencies{State: state, Sender: sender})
    for range 2 {
        result, err := restarted.Serve(ctx, NotifyServeInput{Once: true}, nil)
        var f *fault.Error
        if !errors.As(err, &f) || f.Code != "DELIVERY_UNCONFIRMED" || result.Unconfirmed != 1 || sender.calls != 0 {
            t.Fatal("restart replayed a possible send", result, err, sender.calls)
        }
    }
    detail, err := restarted.(NotificationControlService).ShowDelivery(ctx, id)
    if err != nil || detail.Delivery.State != "unconfirmed" || detail.Delivery.Receipt.Accepted() || detail.Delivery.Receipt.RetrySafe || len(detail.Attempts) != 1 {
        t.Fatal("lost receipt became retry permission", detail, err)
    }
    inbox, err := events.List(ctx, EventListInput{Source: "local", Limit: 10})
    if err != nil || len(inbox.Events) != 1 {
        t.Fatal("recovery lost the saved fact", inbox, err)
    }
}
