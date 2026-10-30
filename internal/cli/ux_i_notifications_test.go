package cli

import (
    "bytes"
    "context"
    "errors"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

type uxINotifyFake struct {
    fakeNotify
    locations                   []client.DestinationRecord
    pages                       []client.SubscriptionPage
    listInputs                  []client.SubscriptionListInput
    deliveries                  []client.Delivery
    detail                      client.DeliveryDetail
    sub                         client.Subscription
    resolved                    []string
    resent, reconciled, removed int
    changes                     []string
    prune                       []client.PruneInput
    preview                     client.NotificationDestinationPreview
    previewErr                  error
}

func (f *uxINotifyFake) Destinations(context.Context) ([]client.DestinationRecord, error) {
    return f.locations, nil
}
func (f *uxINotifyFake) Deliveries(context.Context, int) ([]client.Delivery, error) {
    return f.deliveries, nil
}
func (f *uxINotifyFake) ListSubscriptions(_ context.Context, in client.SubscriptionListInput) (client.SubscriptionPage, error) {
    f.listInputs = append(f.listInputs, in)
    if len(f.pages) == 0 {
        return client.SubscriptionPage{}, nil
    }
    if in.After != "" && len(f.pages) > 1 {
        return f.pages[1], nil
    }
    return f.pages[0], nil
}
func (f *uxINotifyFake) ShowDelivery(context.Context, string) (client.DeliveryDetail, error) {
    return f.detail, nil
}
func (f *uxINotifyFake) ShowSubscription(context.Context, string) (client.Subscription, error) {
    return f.sub, nil
}
func (f *uxINotifyFake) ReconcileDelivery(context.Context, string) (client.Delivery, error) {
    f.reconciled++
    return f.detail.Delivery, nil
}
func (f *uxINotifyFake) ResolveDelivery(_ context.Context, _ string, kind string) (client.Delivery, error) {
    f.resolved = append(f.resolved, kind)
    f.detail.Delivery.State = "resolved"
    f.detail.Delivery.Resolution = &client.UserResolution{Kind: kind}
    return f.detail.Delivery, nil
}
func (f *uxINotifyFake) ResendDelivery(context.Context, string) (client.Delivery, error) {
    f.resent++
    f.detail.Delivery.State = "resolved"
    f.detail.Delivery.Resolution = &client.UserResolution{Kind: "resent", SuccessorID: "child"}
    return client.Delivery{ID: "child", State: "pending", Receipt: agentdelivery.Receipt{State: "pending"}}, nil
}
func (f *uxINotifyFake) ChangeSubscription(_ context.Context, id, status string) (client.SubscriptionChange, error) {
    f.changes = append(f.changes, status)
    f.sub.Status = status
    return client.SubscriptionChange{SubscriptionID: id, Status: status}, nil
}
func (f *uxINotifyFake) RemoveDestination(context.Context, string) error { f.removed++; return nil }
func (f *uxINotifyFake) Prune(_ context.Context, in client.PruneInput) (client.PruneResult, error) {
    f.prune = append(f.prune, in)
    return client.PruneResult{Before: in.Before, DryRun: in.DryRun, Deliveries: 2, Inbox: 1}, nil
}
func (f *uxINotifyFake) PreviewCurrentBinding(context.Context) (client.NotificationDestinationPreview, error) {
    return f.preview, f.previewErr
}
func (f *uxINotifyFake) ProbeDestination(context.Context, string) (client.NotificationDestinationPreview, error) {
    return f.preview, f.previewErr
}
func (f *uxINotifyFake) SubscriptionRecipients(context.Context, string) ([]client.DestinationRecord, error) {
    return []client.DestinationRecord{{Name: "receiver", Kind: "codex", SessionID: "frozen_session"}}, nil
}

type uxIEventsFake struct {
    inputs []client.EventListInput
    page   func(client.EventListInput) (event.Page, error)
}

func (f *uxIEventsFake) List(_ context.Context, in client.EventListInput) (event.Page, error) {
    f.inputs = append(f.inputs, in)
    return f.page(in)
}

func uxICall(t *testing.T, language, input string) (*invocation, *bytes.Buffer) {
    t.Helper()
    o := fixture(t)
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l, err := i18n.New(language, nil)
    if err != nil {
        t.Fatal(err)
    }
    out := &bytes.Buffer{}
    c := &invocation{ui: true, app: client.New(o.Dependencies, settings), options: o.Options, grammar: &grammar{}, output: &presenter{locale: l, out: &bytes.Buffer{}, err: out}}
    c.options.Err = out
    c.input = interaction.New(interaction.Options{Interactive: true, In: strings.NewReader(input), Err: out})
    return c, out
}

func TestUXIBareEntryPreservesExplicitAndFiniteCalls(t *testing.T) {
    for _, tc := range []struct {
        args []string
        want string
    }{
        {[]string{"notify"}, "notify"}, {[]string{"events"}, "events"}, {[]string{"--lang", "zh-Hans", "notify"}, "notify"},
        {[]string{"notify", "--json"}, ""}, {[]string{"notify", "deliveries"}, ""}, {[]string{"events", "list"}, ""},
        {[]string{"events", "--limit", "2"}, ""}, {[]string{"notify", "--limit", "2"}, ""}, {[]string{"events", "--target", "lab"}, ""},
        {[]string{"notify", "--trace"}, ""}, {[]string{"notify", "serve"}, ""}, {[]string{"notify", "channel"}, ""}, {[]string{"notify", "--help"}, ""},
    } {
        parser, err := newParser(&grammar{})
        if err != nil {
            t.Fatal(err)
        }
        tc.args = append([]string{"-i"}, tc.args...)
        if got := uxIHubCommand(parser, tc.args, bootstrap(parser.Model, tc.args)); got != tc.want {
            t.Errorf("%v = %q; want %q", tc.args, got, tc.want)
        }
    }
}

func TestUXIEmptySearchCancelAndUnavailable(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        c, out := uxICall(t, language, "3\nno-match\n5\n")
        n := &uxINotifyFake{}
        c.app.Notify = n
        if err := c.uxIDestinations(context.Background()); err != nil {
            t.Fatal(err)
        }
        if !strings.Contains(out.String(), c.output.locale.Text("ux_i_no_destinations")) || n.serveCalls != 0 {
            t.Fatal(out.String(), n)
        }
        c, _ = uxICall(t, language, "7\n")
        c.app.Notify = n
        if _, err := c.uxINotify(context.Background()); err != nil {
            t.Fatal(err)
        }
        c.policy.Batch = true
        if _, err := c.uxINotify(context.Background()); !errors.Is(err, interaction.ErrNonInteractive) {
            t.Fatal(err)
        }
        c.policy.Batch = false
        c.app.Notify = &fakeNotify{}
        if err := c.uxIBindCurrent(context.Background()); err == nil {
            t.Fatal("missing preview capability accepted")
        }
        c.app.Notify = nil
        if _, err := c.uxINotify(context.Background()); err == nil {
            t.Fatal("missing notify accepted")
        }
    }
}

func TestUXISubscribeRunFreezesIDReplayAndRecipient(t *testing.T) {
    for _, replay := range []bool{true, false} {
        choice := "1"
        if !replay {
            choice = "2"
        }
        c, _ := uxICall(t, "en", "1\n"+choice+"\n\n1\n\n")
        n := &uxINotifyFake{locations: []client.DestinationRecord{{Name: "receiver", Kind: "codex", SessionID: "exact_session"}}}
        c.app.Notify = n
        changed := "other-machine"
        c.grammar.Target = &changed
        if _, err := c.uxISubscribeRun(context.Background(), "r_saved"); err != nil {
            t.Fatal(err)
        }
        in := n.subscription
        if in.Target != nil || in.Subject.ID != "r_saved" || in.Source != "project" || in.Replay != replay || !reflect.DeepEqual(in.Destinations, []string{"receiver"}) || n.serveCalls != 0 {
            t.Fatal(in, n.serveCalls)
        }
    }
    c, _ := uxICall(t, "en", "1\n1\n\n2\n\n2\n\n3\n")
    n := &uxINotifyFake{locations: []client.DestinationRecord{{Name: "receiver", Kind: "webhook"}}}
    c.app.Notify = n
    if _, err := c.uxISubscribeRun(context.Background(), "r_saved"); err != nil {
        t.Fatal(err)
    }
    if n.subscription.Subject.ID != "" {
        t.Fatal("back saved draft", n.subscription)
    }
}

func TestUXIPaginationAndNoMatchAreBounded(t *testing.T) {
    c, _ := uxICall(t, "en", "2\n6\n")
    e := event.Envelope{ID: "e1", Type: "resource.ready", Subject: event.Subject{Kind: "resource", ID: "one"}}
    service := &uxIEventsFake{page: func(in client.EventListInput) (event.Page, error) {
        if in.Cursor == "" {
            return event.Page{Events: []event.Envelope{e}, HasMore: true, NextCursor: "cursor_two"}, nil
        }
        return event.Page{Events: []event.Envelope{e}}, nil
    }}
    c.app.Events = service
    if _, err := c.uxIEventPages(context.Background(), client.EventListInput{Source: "resources", Limit: 5}, false); err != nil {
        t.Fatal(err)
    }
    if len(service.inputs) != 2 || service.inputs[1].Cursor != "cursor_two" {
        t.Fatal(service.inputs)
    }
    c, out := uxICall(t, "zh-Hans", "2\nno-match\n4\n")
    c.app.Events = service
    service.inputs = nil
    service.page = func(in client.EventListInput) (event.Page, error) {
        return event.Page{Events: []event.Envelope{e}}, nil
    }
    if _, err := c.uxIEventPages(context.Background(), client.EventListInput{Source: "resources", Limit: 5}, false); err != nil {
        t.Fatal(err)
    }
    if len(service.inputs) != 1 || !strings.Contains(out.String(), "匹配 0 条") {
        t.Fatal(service.inputs, out.String())
    }
    c, _ = uxICall(t, "en", "2\n6\n")
    n := &uxINotifyFake{pages: []client.SubscriptionPage{{Subscriptions: []client.Subscription{{ID: "s1", Status: "enabled"}}, HasMore: true, NextAfter: "s1"}, {Subscriptions: []client.Subscription{{ID: "s2", Status: "removed"}}}}}
    c.app.Notify = n
    if err := c.uxISubscriptions(context.Background()); err != nil {
        t.Fatal(err)
    }
    if len(n.listInputs) != 2 || n.listInputs[1].After != "s1" {
        t.Fatal(n.listInputs)
    }
}

func TestUXIEventFilterRestoresExplicitBrowsingTarget(t *testing.T) {
    c, _ := uxICall(t, "en", "1\n2\n2\n5\n")
    service := &uxIEventsFake{page: func(in client.EventListInput) (event.Page, error) {
        return event.Page{Events: []event.Envelope{{Type: "run.accepted", Subject: event.Subject{Kind: "run", ID: "r_original"}}}}, nil
    }}
    c.app.Events = service
    target := "original"
    changed := "changed"
    c.grammar.Target = &changed
    if _, err := c.uxIEventPages(context.Background(), client.EventListInput{Source: "project", Target: &target, Limit: 5}, false); err != nil {
        t.Fatal(err)
    }
    if len(service.inputs) != 3 || service.inputs[1].Target != nil || service.inputs[1].Subject.ID != "r_original" || service.inputs[2].Target == nil || *service.inputs[2].Target != "original" {
        t.Fatal(service.inputs)
    }
}

func TestUXIPartialSourceFailureKeepsFactsAndDoesNotAdvance(t *testing.T) {
    c, out := uxICall(t, "en", "\n")
    service := &uxIEventsFake{page: func(in client.EventListInput) (event.Page, error) {
        return event.Page{Events: []event.Envelope{{ID: "e_kept", Type: "resource.ready", Subject: event.Subject{Kind: "resource", ID: "retained_fact"}}}, HasMore: true, NextCursor: "unsafe"}, fault.New("STATE_WRITE_FAILED", "async_failed", nil)
    }}
    c.app.Events = service
    if _, err := c.uxIEventPages(context.Background(), client.EventListInput{Source: "local", Limit: 5}, false); err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(out.String(), "retained_fact") || !strings.Contains(out.String(), "STATE_WRITE_FAILED") || strings.Contains(out.String(), "Next page") || len(service.inputs) != 1 {
        t.Fatal(out.String(), service.inputs)
    }
}

func TestUXIDeliveryEvidenceAndApplicableActions(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        c, _ := uxICall(t, language, "")
        for _, state := range []string{"pending", "written", "unconfirmed", "accepted", "processed"} {
            detail := client.DeliveryDetail{Delivery: client.Delivery{ID: "d1", EventID: "e1", State: "unconfirmed", Receipt: agentdelivery.Receipt{State: state}, Resolution: &client.UserResolution{Kind: "confirmed"}}, Event: event.Envelope{ID: "e1"}, Message: agentdelivery.Message{Text: "PRIVATE_RAW_LOG"}}
            doc := c.uxIDeliveryDocument(detail)
            body := doc.Content(70, false, false)
            if strings.Contains(body, "PRIVATE_RAW_LOG") {
                t.Fatal(body)
            }
            values := map[string]string{}
            for _, r := range doc.Rows {
                values[r.Label] = r.Value
            }
            want := c.output.locale.Text("ux_i_unknown")
            if state == "processed" {
                want = c.output.locale.Text("ux_i_recorded")
            }
            if values[c.output.locale.Text("ux_i_processing")] != want {
                t.Fatal(state, values)
            }
        }
    }
    d := client.DeliveryDetail{Recipient: client.DestinationRecord{Kind: "webhook"}, Delivery: client.Delivery{State: "unconfirmed"}}
    actions := uxIDeliveryActions(d, client.Subscription{Status: "enabled"})
    if strings.Contains(strings.Join(actions, ","), "reconcile") || !strings.Contains(strings.Join(actions, ","), "resend") {
        t.Fatal(actions)
    }
    d.Recipient.Kind = "claude-channel"
    d.Delivery.State = "accepted"
    if actions = uxIDeliveryActions(d, client.Subscription{Status: "disabled"}); reflect.DeepEqual(actions, []string{}) || strings.Contains(strings.Join(actions, ","), "resend") || strings.Contains(strings.Join(actions, ","), "confirmed") {
        t.Fatal(actions)
    }
}

func TestUXIResendRequiresTwoDecisionsAndNeverSends(t *testing.T) {
    for _, tc := range []struct {
        answers string
        want    int
    }{{"n\n", 0}, {"y\nn\n", 0}, {"y\ny\n\n", 1}} {
        c, _ := uxICall(t, "en", "6\n\n"+tc.answers+"\n")
        n := &uxINotifyFake{detail: client.DeliveryDetail{Recipient: client.DestinationRecord{Name: "frozen", Kind: "webhook"}, Delivery: client.Delivery{ID: "d1", State: "unconfirmed", SubscriptionID: "sub", Receipt: agentdelivery.Receipt{State: "written"}}}, sub: client.Subscription{ID: "sub", Status: "enabled"}}
        c.app.Notify = n
        if err := c.uxIDelivery(context.Background(), "d1"); err != nil {
            t.Fatal(err)
        }
        if n.resent != tc.want || n.serveCalls != 0 || n.detail.Delivery.Receipt.State != "written" {
            t.Fatal(tc, n.resent, n.serveCalls, n.detail)
        }
    }
}

func TestUXIResolvePruneAndCurrentBindingAreExplicit(t *testing.T) {
    c, _ := uxICall(t, "en", "4\n\ny\n\n\n")
    n := &uxINotifyFake{detail: client.DeliveryDetail{Recipient: client.DestinationRecord{Kind: "webhook"}, Delivery: client.Delivery{ID: "d1", State: "unconfirmed", Receipt: agentdelivery.Receipt{State: "written"}}}, sub: client.Subscription{Status: "enabled"}}
    c.app.Notify = n
    if err := c.uxIDelivery(context.Background(), "d1"); err != nil || !reflect.DeepEqual(n.resolved, []string{"confirmed"}) || n.detail.Delivery.Receipt.State != "written" {
        t.Fatal(err, n.resolved)
    }
    for _, yes := range []string{"n", "y"} {
        c, _ = uxICall(t, "en", "2026-01-01T00:00:00Z\n\n"+yes+"\n\n")
        n = &uxINotifyFake{}
        c.app.Notify = n
        if err := c.uxIPrune(context.Background()); err != nil {
            t.Fatal(err)
        }
        want := 1
        if yes == "y" {
            want = 2
        }
        if len(n.prune) != want || !n.prune[0].DryRun || want == 2 && n.prune[1].DryRun {
            t.Fatal(n.prune)
        }
    }
    c, out := uxICall(t, "zh-Hans", "\nhere\n\ny\n\n")
    n = &uxINotifyFake{locations: []client.DestinationRecord{{Name: "here", Kind: "codex", SessionID: "previous_session"}}, preview: client.NotificationDestinationPreview{Destination: client.DestinationRecord{Kind: "codex-desktop", SessionID: "exact_session"}, Capabilities: agentdelivery.Capabilities{Available: true, ExistingSession: true}}}
    c.app.Notify = n
    if err := c.uxIBindCurrent(context.Background()); err != nil {
        t.Fatal(err)
    }
    if n.bindingCalls != 1 || !n.binding.Replace || !strings.Contains(out.String(), "previous_session") || !strings.Contains(out.String(), "exact_session") || n.serveCalls != 0 {
        t.Fatal(n.binding, out.String())
    }
}

func TestUXIPrivateInputAndMixedSourceEvidence(t *testing.T) {
    for _, endpoint := range []string{"https://user:password@example.test/hook", "https://example.test/hook?token=SECRET", "https://example.test/hook#SECRET", "/tmp/socket?token=x"} {
        if uxIEndpointValid(endpoint) {
            t.Fatal(endpoint)
        }
    }
    for _, endpoint := range []string{"https://receiver.example/hook", "/tmp/exact.sock"} {
        if !uxIEndpointValid(endpoint) {
            t.Fatal(endpoint)
        }
    }
    if uxIBearerName.MatchString("Bearer secret-token") {
        t.Fatal("token accepted as env name")
    }
    c, _ := uxICall(t, "en", "")
    n := &uxINotifyFake{}
    c.app.Notify = n
    success := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
    failure := success.Add(time.Hour)
    doc, err := c.uxISubscriptionDocument(context.Background(), client.Subscription{ID: "sub", Sources: []client.SubscriptionSource{{ID: "queue_binding", Source: "queue", LastSyncedAt: &success}, {ID: "run_binding", Source: "project", SourceError: "SSH_FAILED", SourceErrorAt: &failure}}})
    if err != nil {
        t.Fatal(err)
    }
    body := doc.Content(70, false, false)
    for _, value := range []string{"queue_binding", "run_binding", "SSH_FAILED", "2026-10-28T01:00:00Z", "frozen_session"} {
        if !strings.Contains(body, value) {
            t.Fatal(value, body)
        }
    }
}

// This acceptance test also checks the exact routing.patch delivered to 00.
// Before consumption, run it with that candidate's Go overlay.
func TestUXIIntegratedBareAndDirectEntries(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, command := range []string{"notify", "events"} {
            o := fixture(t)
            o.Interactive = true
            o.In = strings.NewReader("7\n")
            if command == "events" {
                o.In = strings.NewReader("6\n")
            }
            n, e := &uxINotifyFake{}, &fakeEvents{}
            o.Notify = n
            o.Events = e
            code, out, stderr := run(t, o, "-i", "--lang", lang, command)
            l, _ := i18n.New(lang, nil)
            if code != 0 || out != "" || !strings.Contains(stderr, l.Text("ux_i_"+command)) || n.serveCalls != 0 {
                t.Fatal(command, code, out, stderr)
            }
        }
        for _, args := range [][]string{{"notify", "deliveries"}, {"notify", "--json"}, {"events", "list", "--json"}, {"notify", "--help"}} {
            o := fixture(t)
            o.Interactive = true
            o.In = uxCoreNoInput{t}
            o.Notify = &uxINotifyFake{}
            o.Events = &fakeEvents{}
            code, _, stderr := run(t, o, append([]string{"--lang", lang}, args...)...)
            if code != 0 || stderr != "" {
                t.Fatal(args, code, stderr)
            }
        }
        c, _ := uxICall(t, lang, "7\n")
        c.app.Notify = &uxINotifyFake{}
        if !c.uxRouteAvailable("notify") || !c.uxRouteAvailable("notify-run") {
            t.Fatal("notification route unavailable")
        }
        if _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "notify"}); err != nil {
            t.Fatal(err)
        }
        target := "forbidden"
        if err := (uxRoute{Kind: "notify-run", ID: "r_saved", Target: &target}).validate(); err == nil {
            t.Fatal("history redirected")
        }
    }
}

func TestUXIAddDestinationReviewsSafeInputAndRemovalIsExplicit(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        c, out := uxICall(t, lang, "hook\n1\nhttps://receiver.example/hook?token=PRIVATE_TOKEN\nhttps://receiver.example/hook\nBearer PRIVATE_TOKEN\nHOOK_TOKEN\n\n1\n\n")
        n := &uxINotifyFake{}
        c.app.Notify = n
        c.options.Env["HOOK_TOKEN"] = "PRIVATE_TOKEN"
        if err := c.uxIAddDestination(context.Background()); err != nil {
            t.Fatal(err, out.String())
        }
        d := n.destination
        if d.Name != "hook" || d.Destination.Kind != "webhook" || d.Destination.BearerRef != "HOOK_TOKEN" || d.Destination.Endpoint != "https://receiver.example/hook" || n.serveCalls != 0 {
            t.Fatal(d)
        }
        if strings.Contains(out.String(), "PRIVATE_TOKEN") || !strings.Contains(out.String(), c.output.locale.Text("notify_destination_invalid")) {
            t.Fatal(out.String())
        }
        for _, choice := range []string{"n", "y"} {
            c, _ = uxICall(t, lang, "3\n\n"+choice+"\n\n")
            n = &uxINotifyFake{}
            c.app.Notify = n
            if err := c.uxIDestination(context.Background(), client.DestinationRecord{Name: "hook", Kind: "webhook"}); err != nil {
                t.Fatal(err)
            }
            want := 0
            if choice == "y" {
                want = 1
            }
            if n.removed != want || n.serveCalls != 0 {
                t.Fatal(choice, n.removed, n.serveCalls)
            }
        }
    }
}

func TestUXIPlainTerminalsAndLocalizedDocuments(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, mode := range []string{"NO_COLOR", "TERM"} {
            o := fixture(t)
            o.Interactive, o.TerminalError = true, true
            o.Notify = &uxINotifyFake{}
            o.In = strings.NewReader("7\n")
            o.Env["TERM"] = "xterm-256color"
            o.Env[mode] = "1"
            if mode == "TERM" {
                o.Env[mode] = "dumb"
            }
            code, out, stderr := run(t, o, "-i", "--lang", lang, "notify")
            if code != 0 || out != "" || strings.Contains(stderr, "\x1b[") || !strings.Contains(stderr, "1.") {
                t.Fatal(mode, code, out, stderr)
            }
        }
        c, _ := uxICall(t, lang, "")
        for _, dark := range []bool{false, true} {
            doc := c.uxIDeliveryDocument(client.DeliveryDetail{Delivery: client.Delivery{ID: "delivery_fixture", Receipt: agentdelivery.Receipt{State: "written"}}})
            for _, width := range []int{80, 120} {
                rendered := doc.Render(width, true, dark)
                if !strings.Contains(rendered, "delivery_fixture") || !strings.Contains(rendered, c.output.locale.Text("ux_i_processing")) {
                    t.Fatal(width, dark, rendered)
                }
            }
        }
    }
}
