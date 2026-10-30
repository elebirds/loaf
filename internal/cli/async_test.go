package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

type fakeEvents struct {
    input client.EventListInput
    calls int
}

func (f *fakeEvents) List(_ context.Context, in client.EventListInput) (event.Page, error) {
    f.input = in
    f.calls++
    return event.Page{SourceID: "source_1", Events: []event.Envelope{{ID: "event_1", Type: "resource.ready", Subject: event.Subject{Kind: "resource", ID: "instance_1"}, OccurredAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)}}}, nil
}

type fakeNotify struct {
    binding       client.BindCurrentInput
    bindingCalls  int
    destination   client.DestinationInput
    subscription  client.SubscribeInput
    serveInput    client.NotifyServeInput
    serveCalls    int
    subscriptions []client.Subscription
}

func (f *fakeNotify) BindCurrent(_ context.Context, in client.BindCurrentInput) (client.CurrentBindingResult, error) {
    f.binding, f.bindingCalls = in, f.bindingCalls+1
    return client.CurrentBindingResult{Destination: client.DestinationRecord{Name: in.Name, Kind: "codex-desktop", SessionID: "current_thread"}, VerifiedAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)}, nil
}

func TestNotifyBindCurrentGrammarAndRendering(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        options := fixture(t)
        notify := &fakeNotify{}
        options.Notify = notify
        code, out, stderr := run(t, options, "--lang", language, "notify", "bind", "here", "--current", "--replace", "--json")
        if code != 0 || stderr != "" || !json.Valid([]byte(out)) || notify.binding.Name != "here" || !notify.binding.Replace || !strings.Contains(out, `"session_id":"current_thread"`) || !strings.Contains(out, `"verified_at"`) {
            t.Fatal(code, out, stderr, notify.binding)
        }
        code, out, stderr = run(t, options, "--lang", language, "notify", "bind", "here", "--current")
        if code != 0 || stderr != "" || !strings.Contains(out, "here") || !strings.Contains(out, "current_thread") {
            t.Fatal(code, out, stderr)
        }
        for _, args := range [][]string{{"notify", "bind", "here"}, {"notify", "bind", "here", "--current=false"}, {"notify", "bind", "here", "--current", "--target", "another"}} {
            before := notify.bindingCalls
            code, out, _ := run(t, options, append(args, "--json")...)
            if code != 2 || notify.bindingCalls != before || !json.Valid([]byte(out)) {
                t.Fatal(args, code, out)
            }
        }
    }
}

func (f *fakeNotify) PutDestination(_ context.Context, in client.DestinationInput) (client.DestinationRecord, error) {
    f.destination = in
    return client.DestinationRecord{Name: in.Name, Kind: in.Destination.Kind, SessionID: in.Destination.SessionID}, nil
}
func (f *fakeNotify) Destinations(context.Context) ([]client.DestinationRecord, error) {
    return []client.DestinationRecord{}, nil
}
func (f *fakeNotify) Subscribe(_ context.Context, in client.SubscribeInput) (client.Subscription, error) {
    f.subscription = in
    return client.Subscription{ID: "sub_1", Subject: in.Subject, Source: in.Source, Destinations: in.Destinations}, nil
}
func (f *fakeNotify) ListSubscriptions(ctx context.Context, in client.SubscriptionListInput) (client.SubscriptionPage, error) {
    rows, err := f.Subscriptions(ctx)
    return client.SubscriptionPage{Subscriptions: rows}, err
}
func (f *fakeNotify) Subscriptions(context.Context) ([]client.Subscription, error) {
    if f.subscriptions != nil {
        return f.subscriptions, nil
    }
    return []client.Subscription{}, nil
}
func (f *fakeNotify) Deliveries(context.Context, int) ([]client.Delivery, error) {
    return []client.Delivery{}, nil
}
func (f *fakeNotify) Serve(_ context.Context, in client.NotifyServeInput, emit func(client.Delivery) error) (client.NotifyServeResult, error) {
    f.serveInput = in
    f.serveCalls++
    result := client.NotifyServeResult{Received: 1, Attempted: 1, Pending: 1}
    if emit != nil {
        err := emit(client.Delivery{ID: "delivery_1", EventID: "event_1", Destination: "research", Receipt: agentdelivery.Receipt{State: "written"}})
        if err != nil {
            return result, err
        }
    }
    return result, nil
}

type fakeQueue struct {
    submit   client.QueueSubmitInput
    enabled  client.QueueEnableInput
    list     client.QueueListInput
    paused   client.QueuePauseInput
    shown    client.QueueIDInput
    canceled client.QueueIDInput
    cleans   []client.QueueCleanInput
    err      error
}

func queueFixtureResult() queue.Result {
    return queue.Result{Request: &queue.Request{ID: "q_1", RunID: "r_1", State: "queued"}, Status: &queue.Status{TargetID: "target_1", Slots: 1, Queued: 1, CoordinatorRunning: true}}
}
func (f *fakeQueue) Enable(_ context.Context, in client.QueueEnableInput) (queue.Result, error) {
    f.enabled = in
    return queueFixtureResult(), f.err
}
func (f *fakeQueue) Submit(_ context.Context, in client.QueueSubmitInput, progress client.ProgressFunc) (client.QueueSubmitResult, error) {
    f.submit = in
    if progress != nil {
        progress(client.Progress{Stage: "queue_checking"})
        progress(client.Progress{Stage: "queue_syncing", Count: 3})
    }
    return client.QueueSubmitResult{Request: queueFixtureResult().Request}, f.err
}
func (f *fakeQueue) List(_ context.Context, in client.QueueListInput) (queue.Result, error) {
    f.list = in
    return queueFixtureResult(), f.err
}
func (f *fakeQueue) Show(_ context.Context, in client.QueueIDInput) (queue.Result, error) {
    f.shown = in
    return queueFixtureResult(), f.err
}
func (f *fakeQueue) Cancel(_ context.Context, in client.QueueIDInput) (queue.Result, error) {
    f.canceled = in
    return queueFixtureResult(), f.err
}
func (f *fakeQueue) SetPaused(_ context.Context, in client.QueuePauseInput) (queue.Result, error) {
    f.paused = in
    return queueFixtureResult(), f.err
}
func (f *fakeQueue) Clean(_ context.Context, in client.QueueCleanInput) (queue.Result, error) {
    f.cleans = append(f.cleans, in)
    result := queue.Result{Requests: []queue.Request{{ID: "q_1", State: "canceled"}}}
    if !in.DryRun {
        result.Cleaned = append([]string(nil), in.IDs...)
    }
    return result, f.err
}

func TestQueueHumanCoverageIsExplicit(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        l, err := i18n.New(lang, nil)
        if err != nil {
            t.Fatal(err)
        }
        var out bytes.Buffer
        if err := printQueueResult(&out, queue.Result{Requests: []queue.Request{{ID: "q_1", RunID: "r_1", State: "queued"}}, HasMore: true}, l); err != nil {
            t.Fatal(err)
        }
        if !strings.Contains(out.String(), l.Text("queue_more")) {
            t.Fatal("bounded queue coverage was hidden", out.String())
        }
    }
}

func TestAsyncOptionalServicesAreActionable(t *testing.T) {
    for _, args := range [][]string{{"events", "list", "--json"}, {"notify", "--json"}, {"queue", "list", "--json"}} {
        code, out, stderr := run(t, fixture(t), args...)
        if code != 1 || stderr != "" || !strings.Contains(out, "FEATURE_UNAVAILABLE") || !strings.Contains(out, "scripts/build.sh") {
            t.Fatalf("%v: %d %s %s", args, code, out, stderr)
        }
    }
}

func TestAsyncGrammarDispatchAndPrivateDestination(t *testing.T) {
    o := fixture(t)
    events, notify, q := &fakeEvents{}, &fakeNotify{}, &fakeQueue{}
    o.Events = events
    o.Notify = notify
    o.Queue = q
    calls := [][]string{
        {"events", "list", "--source", "resources", "--subject", "resource:instance_1", "--limit", "7", "--json"},
        {"notify", "destination", "add", "research", "--kind", "codex", "--session", "thread_1", "--endpoint", "/private/tmp/private-endpoint-secret", "--json"},
        {"notify", "subscribe", "run:r_1", "--destination", "research", "--replay=false", "--json"},
        {"queue", "enable", "--slots", "2", "--target", "lab", "--json"},
        {"queue", "submit", "--notify", "research", "--tag", "exp", "--json", "--", "python3", "train.py", "--json", "--notify", "literal"},
        {"queue", "pause", "--target", "lab", "--json"},
        {"queue", "show", "q_1", "--json"},
        {"queue", "cancel", "q_1", "--json"},
    }
    for _, args := range calls {
        code, out, stderr := run(t, o, args...)
        if code != 0 || stderr != "" || !json.Valid([]byte(out)) || strings.Contains(out, "private-endpoint-secret") {
            t.Fatalf("%v: %d %s %s", args, code, out, stderr)
        }
    }
    if events.input.Source != "resources" || events.input.Limit != 7 || events.input.Subject.ID != "instance_1" || !events.input.Policy.Batch {
        t.Fatalf("%+v", events.input)
    }
    if notify.destination.Destination.SessionID != "thread_1" || notify.destination.Destination.Endpoint != "/private/tmp/private-endpoint-secret" || notify.subscription.Replay || notify.subscription.Source != "project" {
        t.Fatalf("%+v", notify)
    }
    want := []string{"python3", "train.py", "--json", "--notify", "literal"}
    if !reflect.DeepEqual(q.submit.Args, want) || !reflect.DeepEqual(q.submit.Notify, []string{"research"}) || q.enabled.Slots != 2 || !q.paused.Paused || q.shown.Target != nil || q.canceled.ID != "q_1" {
        t.Fatalf("%+v", q)
    }
}

func TestAsyncValidationRejectsBeforeServiceEffects(t *testing.T) {
    o := fixture(t)
    e, n, q := &fakeEvents{}, &fakeNotify{}, &fakeQueue{}
    o.Events = e
    o.Notify = n
    o.Queue = q
    for _, args := range [][]string{
        {"events", "--source", "local", "--target", "lab", "--json"},
        {"events", "--limit", "0", "--json"},
        {"events", "--subject", "run:../escape", "--json"},
        {"notify", "subscribe", "resource_operation:c_1", "--source", "resources", "--target", "lab", "--destination", "research", "--json"},
        {"notify", "destination", "add", "research", "--kind", "codex", "--json"},
        {"notify", "serve", "--interval", "0s", "--json"},
        {"queue", "enable", "--slots", "0", "--json"},
        {"queue", "show", "q_1", "--target", "other", "--json"},
        {"queue", "clean", "q_1", "--json"},
    } {
        code, out, stderr := run(t, o, args...)
        if code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatalf("%v: %d %s %s", args, code, out, stderr)
        }
    }
    if e.calls != 0 || n.serveCalls != 0 || q.enabled.Slots != 0 || len(q.cleans) != 0 {
        t.Fatal("invalid request reached service")
    }
}

func TestQueueCleanPreviewAndExplicitConfirmation(t *testing.T) {
    o := fixture(t)
    q := &fakeQueue{}
    o.Queue = q
    code, out, stderr := run(t, o, "queue", "clean", "q_1", "--dry-run", "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, `"dry_run":true`) || len(q.cleans) != 1 || !q.cleans[0].DryRun {
        t.Fatal(code, out, stderr, q.cleans)
    }
    q.cleans = nil
    code, out, stderr = run(t, o, "queue", "clean", "q_1", "--yes", "--json")
    if code != 0 || stderr != "" || len(q.cleans) != 2 || !q.cleans[0].DryRun || q.cleans[1].DryRun || !strings.Contains(out, `"dry_run":false`) {
        t.Fatal(code, out, stderr, q.cleans)
    }
    q.cleans = nil
    o.Interactive = true
    o.In = strings.NewReader("no\n")
    code, _, stderr = run(t, o, "-i", "queue", "clean", "q_1")
    if code != 1 || len(q.cleans) != 1 || !strings.Contains(stderr, "Shared datasets") {
        t.Fatal(code, stderr, q.cleans)
    }
}

func TestNotifyFiniteAndStreamingJSONStayDistinct(t *testing.T) {
    o := fixture(t)
    n := &fakeNotify{}
    o.Notify = n
    code, out, stderr := run(t, o, "notify", "serve", "--once", "--json")
    if code != 0 || stderr != "" || strings.Count(strings.TrimSpace(out), "\n") != 0 || !n.serveInput.Once {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "notify", "serve", "--json")
    if code != 0 || stderr != "" || n.serveInput.Once {
        t.Fatal(code, out, stderr)
    }
    decoder := json.NewDecoder(strings.NewReader(out))
    var types []string
    for {
        var e struct {
            Data struct {
                Type string `json:"type"`
            } `json:"data"`
        }
        err := decoder.Decode(&e)
        if errors.Is(err, io.EOF) {
            break
        }
        if err != nil {
            t.Fatal(err, out)
        }
        types = append(types, e.Data.Type)
    }
    if !reflect.DeepEqual(types, []string{"delivery", "end"}) {
        t.Fatal(types, out)
    }
    code, _, stderr = run(t, o, "notify", "serve", "--trace")
    if code != 2 || !strings.Contains(stderr, "finite") {
        t.Fatal(code, stderr)
    }
}

func TestAsyncUnconfirmedExitAndLocalizedHelp(t *testing.T) {
    for _, code := range []string{"QUEUE_SUBMIT_UNCONFIRMED", "QUEUE_CANCEL_UNCONFIRMED", "QUEUE_CLEAN_UNCONFIRMED", "DELIVERY_UNCONFIRMED"} {
        if got := exitCode(&errorView{Code: code}); got != 3 {
            t.Fatalf("%s: %d", code, got)
        }
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, command := range [][]string{{"queue", "submit"}, {"notify", "destination", "add"}, {"notify", "subscribe"}, {"events", "list"}} {
            args := append([]string{"--lang", lang, "help"}, command...)
            code, out, stderr := run(t, fixture(t), args...)
            if code != 0 || stderr != "" || strings.Contains(out, "flag_") || strings.Contains(out, "_short") {
                t.Fatal(args, code, out, stderr)
            }
        }
    }
    q := &fakeQueue{err: fault.New("QUEUE_SUBMIT_UNCONFIRMED", "queue_operation_failed", map[string]any{"Code": "QUEUE_SUBMIT_UNCONFIRMED"})}
    o := fixture(t)
    o.Queue = q
    code, out, _ := run(t, o, "queue", "submit", "--json", "--", "true")
    if code != 3 || !strings.Contains(out, `"request_id":"q_1"`) {
        t.Fatal(code, out)
    }
}

func TestAsyncConsumerFailureDoesNotWriteAnotherEnvelope(t *testing.T) {
    out := &failAfterWriter{}
    o := fixture(t)
    o.Notify = &fakeNotify{}
    o.Out = out
    o.Err = &bytes.Buffer{}
    if code := execute(context.Background(), []string{"notify", "serve", "--json"}, o); code != 1 {
        t.Fatal(code)
    }
    if out.calls != 1 {
        t.Fatalf("wrote after consumer failure: %d", out.calls)
    }
}

type failAfterWriter struct {
    calls int
}

func (w *failAfterWriter) Write(p []byte) (int, error) { w.calls++; return 0, io.ErrClosedPipe }

func TestSubscriptionsRetainSourceSyncAndFailureTimes(t *testing.T) {
    synced := time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)
    failed := synced.Add(-time.Hour)
    row := client.Subscription{ID: "sub_1", Subject: event.Subject{Kind: "run", ID: "r_1"}, Source: "project", Destinations: []string{"research"}, LastSyncedAt: &synced, SourceError: "SSH_FAILED", SourceErrorAt: &failed}
    for _, lang := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        o.Notify = &fakeNotify{subscriptions: []client.Subscription{row}}
        code, out, stderr := run(t, o, "--lang", lang, "notify", "subscriptions")
        if code != 0 || stderr != "" {
            t.Fatal(code, out, stderr)
        }
        l, _ := i18n.New(lang, nil)
        for _, detail := range []string{
            "sub_1  run:r_1  project  research",
            l.Text("notify_source_synced", map[string]any{"Value": synced.Format(time.RFC3339)}),
            l.Text("notify_source_error_at", map[string]any{"Value": failed.Format(time.RFC3339), "Code": "SSH_FAILED"}),
        } {
            if !strings.Contains(out, detail) {
                t.Fatal(out, detail)
            }
        }
        code, out, stderr = run(t, o, "--lang", lang, "notify", "subscriptions", "--json")
        var result struct {
            Data client.SubscriptionPage `json:"data"`
        }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &result) != nil || len(result.Data.Subscriptions) != 1 || !reflect.DeepEqual(result.Data.Subscriptions[0], row) {
            t.Fatal(code, out, stderr)
        }
        undated := row
        undated.LastSyncedAt, undated.SourceErrorAt = nil, nil
        buffer := &bytes.Buffer{}
        if err := printSubscription(buffer, undated, l); err != nil || !strings.Contains(buffer.String(), l.Text("notify_source_error", map[string]any{"Code": "SSH_FAILED"})) || strings.Contains(buffer.String(), "2026-") {
            t.Fatal(buffer.String(), err)
        }
    }
}

func TestHumanDeliveryShowsDispatchAndReceiptSeparately(t *testing.T) {
    l, _ := i18n.New("en", nil)
    out := &bytes.Buffer{}
    next := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
    row := client.Delivery{ID: "delivery_1", State: "pending", Receipt: agentdelivery.Receipt{State: "rejected", RetrySafe: true}, NextAttemptAt: &next}
    if err := printDelivery(out, row, l); err != nil {
        t.Fatal(err)
    }
    text := out.String()
    for _, wanted := range []string{"pending delivery", "SDK evidence: rejected", "Next safe attempt:"} {
        if !strings.Contains(text, wanted) {
            t.Fatal(text)
        }
    }
    if strings.Contains(text, "processing confirmed") {
        t.Fatal(text)
    }
}

func TestQueueProgressAndConfirmedCloudBindingOfferNextAction(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        o.Queue = &fakeQueue{}
        code, _, stderr := run(t, o, "--lang", lang, "queue", "submit", "--", "true")
        if code != 0 || stderr == "" || strings.Contains(stderr, "_progress") {
            t.Fatal(code, stderr)
        }
        l, _ := i18n.New(lang, nil)
        out := &bytes.Buffer{}
        printCloud(out, cloudBinding{Target: "gpu", Status: "ready"}, l)
        if !strings.Contains(out.String(), "loaf machine use gpu") {
            t.Fatal(out.String())
        }
        out.Reset()
        printCloud(out, cloudBinding{Target: "gpu", Status: "unknown"}, l)
        if strings.Contains(out.String(), "loaf machine use") {
            t.Fatal("offered ready next action for unknown binding", out.String())
        }
        out.Reset()
        if err := printQueueResult(out, queue.Result{StateError: "STATE_WRITE", Request: &queue.Request{ID: "q_1", State: "queued"}}, l); err != nil {
            t.Fatal(err)
        }
        if !strings.Contains(out.String(), "STATE_WRITE") || !strings.Contains(out.String(), "q_1") {
            t.Fatal(out.String())
        }
    }
}

func TestBoundedStorageAndHardwareRendering(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(lang, nil)
        source := &bytes.Buffer{}
        printSync(source, syncResult{DryRun: true, SelectedBytes: 1 << 30, Policy: config.SyncPolicy{Mode: "mirror", RespectGitignore: true}}, l)
        if !strings.Contains(source.String(), "1 GiB") {
            t.Fatal(source.String())
        }
        outputs := &bytes.Buffer{}
        printOutputs(outputs, execution.OutputListing{RunID: "r_1", OutputInventory: wire.OutputInventory{Entries: []wire.OutputEntry{{Type: "file", Size: 10}, {Type: "directory", Size: 4096}, {Type: "symlink", Size: 32}}, Examined: 3, RootsExamined: 1, RootsTotal: 1, Truncated: true, DepthLimited: 1}}, l)
        expectedBytes := l.Text("outputs_observed_bytes", map[string]any{"Count": 1, "Bytes": 10})
        if !strings.Contains(outputs.String(), expectedBytes) {
            t.Fatal(outputs.String())
        }
        clean := &bytes.Buffer{}
        printClean(clean, cleanBatch{DryRun: true, Runs: []execution.CleanResult{{Run: execution.View{ID: "r_1"}, Path: "/managed/runs/r_1"}}}, l)
        if !strings.Contains(clean.String(), "/managed/runs/r_1") || !strings.Contains(clean.String(), l.Text("clean_payload_scope")) {
            t.Fatal(clean.String())
        }
        resources := &bytes.Buffer{}
        printResourceObservation(resources, &ssh.ResourceObservation{ObservedAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC), Unavailable: []string{"cpu", "memory_total", "gpu"}}, l)
        if strings.Contains(resources.String(), "<no value>") || strings.Contains(resources.String(), "probe_component_") || !strings.Contains(resources.String(), "2026-10-28") {
            t.Fatal(resources.String())
        }
        if lang == "en" && !strings.Contains(resources.String(), "unknown") || lang == "zh-Hans" && !strings.Contains(resources.String(), "未知") {
            t.Fatal(resources.String())
        }
        if !strings.Contains(resources.String(), l.Text("probe_cpu", map[string]any{"Count": l.Text("probe_unknown")})) {
            t.Fatal(resources.String())
        }
    }
}
