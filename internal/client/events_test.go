package client

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/eventlog"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

type notificationSender struct {
    calls       int
    unconfirmed bool
    destination agentdelivery.Destination
    messages    []agentdelivery.Message
}

func (s *notificationSender) Probe(context.Context, agentdelivery.Destination) (agentdelivery.Capabilities, error) {
    return agentdelivery.Capabilities{Available: true}, nil
}
func (s *notificationSender) Deliver(_ context.Context, d agentdelivery.Destination, m agentdelivery.Message) (agentdelivery.Receipt, error) {
    s.calls++
    s.destination = d
    s.messages = append(s.messages, m)
    if s.unconfirmed {
        return agentdelivery.Receipt{MessageID: m.ID, State: "unconfirmed", ReasonCode: "SEND_UNCONFIRMED"}, agentdelivery.ErrUnconfirmed
    }
    return agentdelivery.Receipt{MessageID: m.ID, State: "accepted", OperationID: "accepted_1"}, nil
}

func notificationFixture(t *testing.T, sender agentdelivery.Sender) (*local.Store, EventService, NotificationService) {
    t.Helper()
    state := local.New(t.TempDir())
    events, notify := NewEventServices(EventDependencies{State: state, CWD: t.TempDir(), Sender: sender})
    _, err := notify.PutDestination(context.Background(), DestinationInput{Name: "agent", Destination: agentdelivery.Destination{Kind: "codex", SessionID: "thread_1"}})
    if err != nil {
        t.Fatal(err)
    }
    return state, events, notify
}

func appendResourceEvent(t *testing.T, state *local.Store) {
    t.Helper()
    database, err := db.Open(context.Background(), state.Path, true)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    tx, _ := database.Begin()
    _, err = eventlog.Append(context.Background(), tx, "resource:ready", event.Envelope{Type: "resource.ready", Subject: event.Subject{Kind: "resource", ID: "autodl:pro:main:instance_1"}, Correlation: event.Correlation{CloudRequestID: "c_1"}, Data: event.Data{Status: "ready", InstanceID: "instance_1", Product: "pro"}})
    if err != nil {
        tx.Rollback()
        t.Fatal(err)
    }
    if err = tx.Commit(); err != nil {
        t.Fatal(err)
    }
}

func TestResourceNotificationReplaysOnceAndHidesDestination(t *testing.T) {
    sender := &notificationSender{}
    state, events, notify := notificationFixture(t, sender)
    appendResourceEvent(t, state)
    ctx := context.Background()
    _, err := notify.Subscribe(ctx, SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}, Source: "resources", Destinations: []string{"agent"}, Replay: true})
    if err != nil {
        t.Fatal(err)
    }
    result, err := notify.Serve(ctx, NotifyServeInput{Once: true}, nil)
    if err != nil || result.Received != 1 || result.Accepted != 1 || sender.calls != 1 {
        t.Fatal(result, err, sender.calls)
    }
    if _, err = notify.Serve(ctx, NotifyServeInput{Once: true}, nil); err != nil || sender.calls != 1 {
        t.Fatal("duplicate observation resent message", err, sender.calls)
    }
    page, err := events.List(ctx, EventListInput{Source: "local", Limit: 100})
    if err != nil || len(page.Events) != 1 {
        t.Fatal(page, err)
    }
    records, err := notify.Deliveries(ctx, 100)
    if err != nil || len(records) != 1 || records[0].State != "accepted" || records[0].Receipt.State != "accepted" {
        t.Fatal(records, err)
    }
    data, _ := json.Marshal(records)
    if strings.Contains(string(data), "thread_1") || strings.Contains(string(data), "endpoint") {
        t.Fatal("private binding leaked", string(data))
    }
    if !strings.Contains(sender.messages[0].Text, "c_1") || strings.Contains(sender.messages[0].Text, "env") {
        t.Fatal(sender.messages)
    }
}

func TestUnconfirmedAgentSendRetainedWithoutReplay(t *testing.T) {
    sender := &notificationSender{unconfirmed: true}
    state, _, notify := notificationFixture(t, sender)
    ctx := context.Background()
    notify.Subscribe(ctx, SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}, Source: "resources", Destinations: []string{"agent"}, Replay: true})
    appendResourceEvent(t, state)
    result, err := notify.Serve(ctx, NotifyServeInput{Once: true}, nil)
    var unconfirmed *fault.Error
    if !errors.As(err, &unconfirmed) || unconfirmed.Code != "DELIVERY_UNCONFIRMED" || result.Unconfirmed != 1 || sender.calls != 1 {
        t.Fatal(result, err, sender.calls)
    }
    if _, err = notify.Serve(ctx, NotifyServeInput{Once: true}, nil); !errors.As(err, &unconfirmed) || unconfirmed.Code != "DELIVERY_UNCONFIRMED" || sender.calls != 1 {
        t.Fatal("ambiguous Agent turn repeated", err, sender.calls)
    }
    deliveries, _ := notify.Deliveries(ctx, 100)
    if deliveries[0].State != "unconfirmed" || deliveries[0].Receipt.RetrySafe {
        t.Fatal(deliveries)
    }
}

type frozenEventRemote struct {
    page        event.Page
    destination string
    failures    int
    calls       int
}

func (r *frozenEventRemote) ControlResponse(_ context.Context, target config.Target, _ string, request wire.Request, _ bool) (wire.Response, error) {
    r.destination = target.Destination
    r.calls++
    if r.calls <= r.failures {
        return wire.Response{}, errors.New("unavailable source")
    }
    page := r.page
    if request.Events.Cursor != "" {
        page.Events = []event.Envelope{}
    }
    return wire.Response{Events: &page}, nil
}

func TestBridgeRetainsSourceErrorAndContinuesAfterRecovery(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    state := local.New(t.TempDir())
    root, _, _ := config.Discover(ctx, t.TempDir())
    project, err := state.Select(ctx, root, "lab")
    if err != nil {
        t.Fatal(err)
    }
    ref := task.RunRef{ID: "r_000000000000000000000002", ProjectID: project.ID, Target: "lab", Connection: config.Target{Destination: "bound-host"}, RemoteDir: "/remote/project", Runner: "/runner", Version: wire.Version, CreatedAt: time.Now().UTC(), Submission: "preparing"}
    if err = state.SaveRun(ctx, ref); err != nil {
        t.Fatal(err)
    }
    fact := event.Envelope{EventVersion: 1, ID: "evt_2", SourceID: "source_2", Sequence: 1, Type: "run.succeeded", Subject: event.Subject{Kind: "run", ID: ref.ID}, OccurredAt: time.Now(), DataVersion: 1}
    cursor, _ := event.EncodeCursor(event.Cursor{Version: 1, SourceID: fact.SourceID, Sequence: 1})
    remote := &frozenEventRemote{page: event.Page{SourceID: fact.SourceID, Events: []event.Envelope{fact}, NextCursor: cursor}, failures: 1}
    sender := &notificationSender{}
    _, notify := NewEventServices(EventDependencies{State: state, CWD: root, Remote: remote, Sender: sender})
    notify.PutDestination(ctx, DestinationInput{Name: "agent", Destination: agentdelivery.Destination{Kind: "codex", SessionID: "thread_1"}})
    if _, err = notify.Subscribe(ctx, SubscribeInput{Subject: fact.Subject, Source: "project", Destinations: []string{"agent"}, Replay: true}); err != nil {
        t.Fatal(err)
    }
    result, err := notify.Serve(ctx, NotifyServeInput{Interval: time.Millisecond}, func(Delivery) error { cancel(); return nil })
    if !errors.Is(err, context.Canceled) || result.Accepted != 1 || sender.calls != 1 || remote.calls < 2 {
        t.Fatal(result, err, sender.calls, remote.calls)
    }
    subscriptions, err := notify.Subscriptions(context.Background())
    if err != nil || len(subscriptions) != 1 || subscriptions[0].LastSyncedAt == nil || subscriptions[0].SourceError != "EVENT_FAILED" || subscriptions[0].SourceErrorAt == nil || subscriptions[0].LastSyncedAt.Before(*subscriptions[0].SourceErrorAt) {
        t.Fatal("source recovery evidence lost", subscriptions, err)
    }
}

func TestRunSubscriptionUsesFrozenTargetAfterAliasChanges(t *testing.T) {
    ctx := context.Background()
    state := local.New(t.TempDir())
    root := t.TempDir()
    root, _, _ = config.Discover(ctx, root)
    project, err := state.Select(ctx, root, "lab")
    if err != nil {
        t.Fatal(err)
    }
    ref := task.RunRef{ID: "r_000000000000000000000001", ProjectID: project.ID, Target: "lab", Connection: config.Target{Destination: "original-host", RemoteBase: "~/projects"}, RemoteDir: "/remote/project", Runner: "/runner", Version: wire.Version, CreatedAt: time.Now().UTC(), Submission: "preparing"}
    if err = state.SaveRun(ctx, ref); err != nil {
        t.Fatal(err)
    }
    fact := event.Envelope{EventVersion: 1, ID: "evt_1", SourceID: "source_1", Sequence: 1, Type: "run.succeeded", Subject: event.Subject{Kind: "run", ID: ref.ID}, Correlation: event.Correlation{RunID: ref.ID}, OccurredAt: time.Now(), DataVersion: 1, Data: event.Data{Status: "succeeded"}}
    cursor, _ := event.EncodeCursor(event.Cursor{Version: 1, SourceID: fact.SourceID, Sequence: 1})
    remote := &frozenEventRemote{page: event.Page{SourceID: fact.SourceID, Events: []event.Envelope{fact}, NextCursor: cursor}}
    sender := &notificationSender{}
    _, notify := NewEventServices(EventDependencies{State: state, CWD: root, Remote: remote, Settings: config.Global{Targets: map[string]config.Target{"lab": {Destination: "replacement-host"}}}, Sender: sender})
    notify.PutDestination(ctx, DestinationInput{Name: "agent", Destination: agentdelivery.Destination{Kind: "codex", SessionID: "thread_1"}})
    _, err = notify.Subscribe(ctx, SubscribeInput{Subject: fact.Subject, Source: "project", Destinations: []string{"agent"}, Replay: true})
    if err != nil {
        t.Fatal(err)
    }
    if _, err = notify.Serve(ctx, NotifyServeInput{Once: true}, nil); err != nil || remote.destination != "original-host" || sender.calls != 1 {
        t.Fatal(remote.destination, err, sender.calls)
    }
}

func TestNotificationValidationDoesNotWriteStateOrStartWork(t *testing.T) {
    state := local.New(t.TempDir())
    _, notify := NewEventServices(EventDependencies{State: state, CWD: t.TempDir()})
    _, err := notify.PutDestination(context.Background(), DestinationInput{Name: "agent", Destination: agentdelivery.Destination{Kind: "codex"}})
    if err == nil {
        t.Fatal("ambiguous destination accepted")
    }
    if _, err = os.Stat(state.Path); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("invalid input wrote state", err)
    }
}
