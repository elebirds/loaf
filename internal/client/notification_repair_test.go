package client

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/queue"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/state/notifications"
    queuestate "github.com/elebirds/loaf/internal/state/queue"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

type repairedEventRemote struct {
    store   *queuestate.Store
    runID   string
    mu      sync.Mutex
    targets []string
}

func (r *repairedEventRemote) ControlResponse(ctx context.Context, target config.Target, _ string, req wire.Request, _ bool) (wire.Response, error) {
    r.mu.Lock()
    r.targets = append(r.targets, target.Destination)
    r.mu.Unlock()
    if req.Events.Source == "queue" {
        page, err := r.store.Events(ctx, *req.Events)
        return wire.Response{Events: &page}, err
    }
    fact := event.Envelope{EventVersion: 1, ID: "evt_run_completed", SourceID: "src_project", Sequence: 1, Type: "run.succeeded", Subject: event.Subject{Kind: "run", ID: r.runID}, OccurredAt: time.Now().UTC(), DataVersion: 1, Correlation: event.Correlation{RunID: r.runID}}
    cursor, _ := event.EncodeCursor(event.Cursor{Version: 1, SourceID: fact.SourceID, Sequence: 1})
    page := event.Page{SourceID: fact.SourceID, Events: []event.Envelope{fact}, NextCursor: cursor}
    if req.Events.Cursor != "" {
        page.Events = []event.Envelope{}
    }
    return wire.Response{Events: &page}, nil
}
func realQueueNotifications(t *testing.T) (*Queues, *queueRemote, *eventServices, *notificationSender) {
    t.Helper()
    q, remote, _, _, _, root := queueClientFixture(t)
    sender := &notificationSender{}
    _, notify := NewEventServices(EventDependencies{State: q.Projects.State, Settings: q.Projects.Settings, CWD: root, Runs: q.Projects.Runs, Sender: sender})
    if _, err := notify.PutDestination(context.Background(), DestinationInput{Name: "audit", Destination: agentdelivery.Destination{Kind: "webhook", Endpoint: "http://127.0.0.1:1/events"}}); err != nil {
        t.Fatal(err)
    }
    q.Notify = notify
    return q, remote, notify.(*eventServices), sender
}
func TestQueueNotifyRealServiceAtomicIntentAndBothSources(t *testing.T) {
    q, remote, notify, sender := realQueueNotifications(t)
    ctx := context.Background()
    result, err := q.Submit(ctx, QueueSubmitInput{Notify: []string{"audit"}}, nil)
    if err != nil || result.Request == nil || result.Request.State != "queued" {
        t.Fatal(result, err)
    }
    subs, err := notify.Subscriptions(ctx)
    if err != nil || len(subs) != 1 || len(subs[0].Sources) != 2 {
        t.Fatal("not one atomic multi-source intent", subs, err)
    }
    binding, _ := q.Projects.State.Run(result.Request.ProjectID, result.Request.RunID)
    again, err := notify.RegisterQueuedRun(ctx, QueuedNotificationInput{Request: *result.Request, Run: binding, Destinations: []string{"audit"}})
    if err != nil || again.ID != subs[0].ID {
        t.Fatal(again, err)
    }
    // Default changes cannot redirect either stream.
    notify.dependencies.Settings.Targets["lab"] = config.Target{Destination: "wrong-host"}
    events := &repairedEventRemote{store: remote.store, runID: binding.ID}
    notify.dependencies.Remote = events
    if _, err = remote.store.Cancel(ctx, result.Request.ID); err != nil {
        t.Fatal(err)
    }
    served, err := notify.Serve(ctx, NotifyServeInput{Once: true, Policy: CallPolicy{Batch: true}}, nil)
    if err != nil || served.Accepted != 2 || sender.calls != 2 {
        t.Fatal(served, err, sender.messages)
    }
    events.mu.Lock()
    defer events.mu.Unlock()
    for _, target := range events.targets {
        if target != "original-host" {
            t.Fatal("source redirected", target)
        }
    }
    rows, _ := notify.Deliveries(ctx, 10)
    if len(rows) != 2 {
        t.Fatal(rows)
    }
}
func TestQueueNotifySecondSourceFailureLeavesNoPartialSubscription(t *testing.T) {
    q, remote, notify, _ := realQueueNotifications(t)
    ctx := context.Background()
    database, err := db.Open(ctx, notify.store.Path, true)
    if err != nil {
        t.Fatal(err)
    }
    _, err = database.Exec(`CREATE TRIGGER reject_project BEFORE INSERT ON notify_sources WHEN json_extract(NEW.data,'$.Source')='project' BEGIN SELECT RAISE(ABORT,'injected'); END`)
    database.Close()
    if err != nil {
        t.Fatal(err)
    }
    result, err := q.Submit(ctx, QueueSubmitInput{Notify: []string{"audit"}}, nil)
    if err == nil || result.Request == nil || result.Run == nil || result.Stage != "registering_notifications" || result.NextAction != "inspect_original_request" {
        t.Fatal(result, err)
    }
    subs, listErr := notify.Subscriptions(ctx)
    if listErr != nil || len(subs) != 0 {
        t.Fatal("partial subscription", subs, listErr)
    }
    if len(*remote.order) != 1 || (*remote.order)[0] != "status" {
        t.Fatal("remote side effect before registration", *remote.order)
    }
    canceled, cancelErr := q.Cancel(ctx, QueueIDInput{ID: result.Request.ID})
    if cancelErr != nil || canceled.Request.State != "canceled" {
        t.Fatal("original unprepared identity not cancellable", canceled, cancelErr)
    }
}

type signalingSender struct {
    notificationSender
    delivered chan struct{}
    started   <-chan struct{}
    once      sync.Once
}

func (s *signalingSender) Deliver(ctx context.Context, d agentdelivery.Destination, m agentdelivery.Message) (agentdelivery.Receipt, error) {
    if s.started != nil {
        select {
        case <-s.started:
        case <-ctx.Done():
            return agentdelivery.Receipt{MessageID: m.ID, State: "unconfirmed"}, ctx.Err()
        }
    }
    s.once.Do(func() { close(s.delivered) })
    return agentdelivery.Receipt{MessageID: m.ID, State: "accepted"}, nil
}

type blockedRemote struct {
    started chan struct{}
    once    sync.Once
}

func (r *blockedRemote) ControlResponse(ctx context.Context, _ config.Target, _ string, _ wire.Request, _ bool) (wire.Response, error) {
    r.once.Do(func() { close(r.started) })
    <-ctx.Done()
    return wire.Response{}, ctx.Err()
}
func TestBridgeSlowSourceDoesNotBlockAlreadyReceivedDelivery(t *testing.T) {
    remote := &blockedRemote{started: make(chan struct{})}
    sender := &signalingSender{delivered: make(chan struct{}), started: remote.started}
    state, _, notify := notificationFixture(t, sender)
    s := notify.(*eventServices)
    s.dependencies.Remote = remote
    ctx := context.Background()
    _, err := notify.Subscribe(ctx, SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}, Source: "resources", Destinations: []string{"agent"}, Replay: true})
    if err != nil {
        t.Fatal(err)
    }
    appendResourceEvent(t, state)
    subs, _ := s.store.Subscriptions(ctx)
    page, _ := s.localFacts(ctx, event.ReadInput{Limit: 100, Subject: &subs[0].Subject})
    if _, err = s.store.Collect(ctx, subs[0], page); err != nil {
        t.Fatal(err)
    }
    _, err = s.store.Register(ctx, notifications.Subscription{ID: "sub_blocked", Subject: event.Subject{Kind: "run", ID: "r_blocked"}, Source: "project", Binding: task.RunRef{Runner: "/runner", RemoteDir: "/remote/project", Version: wire.Version}, Destinations: subs[0].Destinations, CreatedAt: time.Now().UTC()}, nil)
    if err != nil {
        t.Fatal(err)
    }
    // Fixture I/O is not part of the concurrent dispatch deadline.
    ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    done := make(chan error, 1)
    go func() {
        _, err := notify.Serve(ctx, NotifyServeInput{Interval: 50 * time.Millisecond, Policy: CallPolicy{Batch: true}}, nil)
        done <- err
    }()
    select {
    case <-sender.delivered:
        cancel()
    case <-ctx.Done():
        t.Fatal("slow source blocked dispatch")
    }
    if err = <-done; !errors.Is(err, context.Canceled) {
        t.Fatal(err)
    }
}
func TestEmptyBridgeDiscoversNewSubscriptionWithoutRestart(t *testing.T) {
    sender := &signalingSender{delivered: make(chan struct{})}
    state, _, notify := notificationFixture(t, sender)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    done := make(chan error, 1)
    go func() {
        _, err := notify.Serve(ctx, NotifyServeInput{Interval: 50 * time.Millisecond}, nil)
        done <- err
    }()
    appendResourceEvent(t, state)
    if _, err := notify.Subscribe(ctx, SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}, Source: "resources", Destinations: []string{"agent"}, Replay: true}); err != nil {
        t.Fatal(err)
    }
    select {
    case <-sender.delivered:
        cancel()
    case err := <-done:
        t.Fatal("empty Bridge exited", err)
    case <-ctx.Done():
        t.Fatal(ctx.Err())
    }
    <-done
}
func TestRecoveryViewsRedactAddressAndPreserveUserDecision(t *testing.T) {
    sender := &notificationSender{unconfirmed: true}
    state, _, notify := notificationFixture(t, sender)
    ctx := context.Background()
    notify.Subscribe(ctx, SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}, Source: "resources", Destinations: []string{"agent"}, Replay: true})
    appendResourceEvent(t, state)
    notify.Serve(ctx, NotifyServeInput{Once: true}, nil)
    rows, _ := notify.Deliveries(ctx, 10)
    control := notify.(NotificationControlService)
    detail, err := control.ShowDelivery(ctx, rows[0].ID)
    if err != nil || len(detail.Attempts) != 1 {
        t.Fatal(detail, err)
    }
    if _, err = control.ReconcileDelivery(ctx, rows[0].ID); err == nil {
        t.Fatal("unsupported lookup hidden")
    }
    resolved, err := control.ResolveDelivery(ctx, rows[0].ID, "confirmed")
    if err != nil || resolved.Receipt.State != "unconfirmed" || resolved.Resolution == nil {
        t.Fatal(resolved, err)
    }
    encoded, _ := json.Marshal(detail)
    var public map[string]any
    if json.Unmarshal(encoded, &public) != nil {
        t.Fatal(string(encoded))
    }
    var f *fault.Error
    if errors.As(err, &f) {
        t.Fatal(f)
    }
}

func TestQueueNotifyAttentionFromRealLedger(t *testing.T) {
    q, remote, notify, sender := realQueueNotifications(t)
    ctx := context.Background()
    result, err := q.Submit(ctx, QueueSubmitInput{Notify: []string{"audit"}}, nil)
    if err != nil {
        t.Fatal(err)
    }
    selected, err := remote.store.Reserve(ctx, queue.FIFO{})
    if err != nil || len(selected) != 1 {
        t.Fatal(selected, err)
    }
    if _, err = remote.store.MarkAttempted(ctx, result.Request.ID); err != nil {
        t.Fatal(err)
    }
    if _, err = remote.store.Unconfirmed(ctx, result.Request.ID, "QUEUE_DISPATCH_UNCONFIRMED"); err != nil {
        t.Fatal(err)
    }
    notify.dependencies.Remote = &repairedEventRemote{store: remote.store, runID: result.Request.RunID}
    served, err := notify.Serve(ctx, NotifyServeInput{Once: true, Policy: CallPolicy{Batch: true}}, nil)
    if err != nil || served.Accepted != 2 {
        t.Fatal(served, err)
    }
    found := false
    for _, m := range sender.messages {
        found = found || m.Subject == "queue.attention_required"
    }
    if !found {
        t.Fatal("missing actionable queue event", sender.messages)
    }
    entry, _ := remote.store.Entry(ctx, result.Request.ID)
    if entry.Request.State != "unconfirmed" || !entry.Attempted {
        t.Fatal("notification altered execution uncertainty", entry)
    }
}

func TestMultiSourceDiagnosticsDoNotMixFailureAndSuccessFromDifferentStreams(t *testing.T) {
    now := time.Now().UTC()
    later := now.Add(time.Second)
    result := subscriptionRecord(notifications.Subscription{ID: "sub_two", Sources: []notifications.Source{{ID: "a", Source: "queue", SourceError: "EVENT_FAILED", SourceErrorAt: &now}, {ID: "b", Source: "project", LastSyncedAt: &later}}})
    if result.LastSyncedAt != nil || result.SourceError != "" || len(result.Sources) != 2 || result.Sources[0].SourceError != "EVENT_FAILED" {
        t.Fatal("cross-source recovery falsely inferred", result)
    }
}

func TestWebhookBearerUsesOnlyInjectedEnvironmentAndRejectsBeforeSend(t *testing.T) {
    ctx := context.Background()
    state := local.New(t.TempDir())
    calls := 0
    const token = "fixture-token-do-not-persist"
    server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls++
        if r.Header.Get("Authorization") != "Bearer "+token {
            t.Error("missing authorization")
        }
        w.WriteHeader(202)
    }))
    defer server.Close()
    available := true
    _, notify := NewEventServices(EventDependencies{State: state, CWD: t.TempDir(), LookupEnv: func(name string) (string, bool) {
        if name != "HOOK_TOKEN" {
            t.Fatal(name)
        }
        return token, available
    }})
    s := notify.(*eventServices)
    sender := s.dependencies.Sender.(agentdelivery.Client)
    sender.Webhook.Transport = server.Client().Transport
    s.dependencies.Sender = sender
    if _, err := notify.PutDestination(ctx, DestinationInput{Name: "auth", Destination: agentdelivery.Destination{Kind: "webhook", Endpoint: server.URL, BearerRef: "HOOK_TOKEN"}}); err != nil {
        t.Fatal(err)
    }
    if _, err := notify.Subscribe(ctx, SubscribeInput{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}, Source: "resources", Destinations: []string{"auth"}, Replay: true}); err != nil {
        t.Fatal(err)
    }
    appendResourceEvent(t, state)
    available = false
    result, err := notify.Serve(ctx, NotifyServeInput{Once: true}, nil)
    var classified *fault.Error
    if !errors.As(err, &classified) || classified.Code != "DELIVERY_REJECTED" || result.Pending != 1 || result.Rejected != 1 || calls != 0 {
        t.Fatal("credential rejection hidden or sent", result, err, calls)
    }
    records, _ := s.store.Deliveries(ctx, 10)
    data, _ := json.Marshal(records)
    if strings.Contains(string(data), token) {
        t.Fatal("token persisted")
    }
    // Retry through the current SDK/application mapping without another source read.
    available = true
    receipt, err := s.dependencies.Sender.Deliver(ctx, transportAddress(records[0].Destination.Address), transportMessage(records[0].Message))
    if err != nil || receipt.State != "accepted" || calls != 1 {
        t.Fatal(receipt, err, calls)
    }
}
