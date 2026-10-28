package cli

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/queue"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

func asyncUnavailable(name string) error {
    return fault.New("FEATURE_UNAVAILABLE", "async_unavailable", map[string]any{"Value": name})
}

func parseSubject(text string) (event.Subject, error) {
    parts := strings.SplitN(text, ":", 2)
    if len(parts) != 2 {
        return event.Subject{}, fault.Invalid("subject", "invalid_value", "event_subject_invalid")
    }
    subject := event.Subject{Kind: parts[0], ID: parts[1]}
    if subject.Validate() != nil {
        return event.Subject{}, fault.Invalid("subject", "invalid_value", "event_subject_invalid")
    }
    return subject, nil
}

func boundedAsyncLimit(value int) error {
    if value < 1 || value > 1000 {
        return fault.Invalid("--limit", "out_of_range", "async_limit")
    }
    return nil
}

func (c *invocation) async(ctx context.Context, path string) (any, error) {
    g, p := c.grammar, c.output
    if path == "events" || path == "events list" {
        v := g.Events.List
        if err := boundedAsyncLimit(v.Limit); err != nil {
            return nil, err
        }
        if (v.Source == "local" || v.Source == "resources") && g.Target != nil {
            return nil, fault.Invalid("--target", "conflicting_arguments", "event_local_target")
        }
        var subject *event.Subject
        if v.Subject != "" {
            parsed, err := parseSubject(v.Subject)
            if err != nil {
                return nil, err
            }
            subject = &parsed
        }
        if c.app == nil || c.app.Events == nil {
            return nil, asyncUnavailable("events")
        }
        return c.app.Events.List(ctx, client.EventListInput{Target: g.Target, Source: v.Source, Cursor: v.Cursor, Limit: v.Limit, Subject: subject, Policy: c.policy})
    }
    if c.app == nil || c.app.Notify == nil {
        return nil, asyncUnavailable("notify")
    }
    service := c.app.Notify
    if strings.HasPrefix(path, "notify delivery ") || strings.HasPrefix(path, "notify subscription ") || path == "notify destination remove" || path == "notify prune" {
        control, ok := service.(client.NotificationControlService)
        if !ok {
            return nil, asyncUnavailable(path)
        }
        switch path {
        case "notify delivery show":
            return control.ShowDelivery(ctx, g.Notify.Delivery.Show.ID)
        case "notify delivery reconcile":
            return control.ReconcileDelivery(ctx, g.Notify.Delivery.Reconcile.ID)
        case "notify delivery resolve":
            return control.ResolveDelivery(ctx, g.Notify.Delivery.Resolve.ID, g.Notify.Delivery.Resolve.As)
        case "notify delivery resend":
            v := g.Notify.Delivery.Resend
            if !v.AllowDuplicate {
                return nil, fault.Invalid("--allow-duplicate", "missing_argument", "notify_duplicate_required")
            }
            if c.policy.Batch && !v.Yes {
                return nil, fault.Invalid("--yes", "missing_argument", "notify_resend_yes_required")
            }
            if !v.Yes {
                detail, err := control.ShowDelivery(ctx, v.ID)
                if err != nil {
                    return detail, err
                }
                recipient := detail.Delivery.Destination + " (" + detail.Recipient.Kind + " " + detail.Recipient.SessionID + ")"
                yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: p.locale.Text("notify_resend_confirm", map[string]any{"ID": v.ID, "Value": recipient}), Kind: interaction.ConfirmDefaultNo, AcceptLabel: p.locale.Text("yes"), RejectLabel: p.locale.Text("no")})
                if err != nil {
                    return detail, err
                }
                if !yes {
                    return nil, fault.New("NOTIFY_CANCELED", "notify_canceled", nil)
                }
            }
            return control.ResendDelivery(ctx, v.ID)
        case "notify subscription show":
            return control.ShowSubscription(ctx, g.Notify.Subscription.Show.ID)
        case "notify subscription disable":
            return control.ChangeSubscription(ctx, g.Notify.Subscription.Disable.ID, "disabled")
        case "notify subscription enable":
            return control.ChangeSubscription(ctx, g.Notify.Subscription.Enable.ID, "enabled")
        case "notify subscription remove":
            return control.ChangeSubscription(ctx, g.Notify.Subscription.Remove.ID, "removed")
        case "notify destination remove":
            name := g.Notify.Destination.Remove.Name
            err := control.RemoveDestination(ctx, name)
            return destinationRemoval{Name: name, Removed: err == nil}, err
        case "notify prune":
            v := g.Notify.Prune
            before, err := time.Parse(time.RFC3339Nano, v.Before)
            if err != nil {
                return nil, fault.Invalid("--before", "invalid_value", "notify_before_invalid")
            }
            if !v.DryRun && c.policy.Batch && !v.Yes {
                return nil, fault.Invalid("--yes", "missing_argument", "notify_prune_yes_required")
            }
            preview, err := control.Prune(ctx, client.PruneInput{Before: before, DryRun: true})
            if err != nil || v.DryRun {
                return preview, err
            }
            if !v.Yes {
                yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: p.locale.Text("notify_prune_confirm", map[string]any{"Count": preview.Deliveries, "Total": preview.Inbox}), Kind: interaction.ConfirmDefaultNo, AcceptLabel: p.locale.Text("yes"), RejectLabel: p.locale.Text("no")})
                if err != nil {
                    return preview, err
                }
                if !yes {
                    return preview, fault.New("NOTIFY_CANCELED", "notify_canceled", nil)
                }
            }
            return control.Prune(ctx, client.PruneInput{Before: before})
        }
    }
    switch path {
    case "notify bind":
        if !g.Notify.Bind.Current {
            return nil, fault.Invalid("--current", "missing_argument", "notify_current_required")
        }
        binder, ok := service.(client.CurrentBindingService)
        if !ok {
            return nil, asyncUnavailable("notify bind")
        }
        return binder.BindCurrent(ctx, client.BindCurrentInput{Name: g.Notify.Bind.Name, Replace: g.Notify.Bind.Replace})
    case "notify channel":
        channel, ok := service.(client.ChannelService)
        if !ok {
            return nil, asyncUnavailable("notify channel")
        }
        return nil, channel.Channel(ctx, client.ChannelInput{Socket: g.Notify.Channel.Socket, SessionID: g.Notify.Channel.Session}, client.Streams{In: c.options.In, Out: p.out, Err: p.err})
    case "notify destination", "notify destination list":
        return service.Destinations(ctx)
    case "notify destination add":
        v := g.Notify.Destination.Add
        destination := agentdelivery.Destination{Kind: v.Kind, Endpoint: v.Endpoint, SessionID: v.Session, CWD: v.CWD, BearerRef: v.BearerEnv, AllowInsecureHTTP: v.AllowInsecureHTTP}
        if destination.Validate() != nil {
            return nil, fault.Invalid("--kind/--session/--endpoint", "invalid_value", "notify_destination_invalid")
        }
        return service.PutDestination(ctx, client.DestinationInput{Name: v.Name, Destination: destination, Replace: v.Replace})
    case "notify subscribe":
        v := g.Notify.Subscribe
        subject, err := parseSubject(v.Subject)
        if err != nil {
            return nil, err
        }
        if (v.Source == "local" || v.Source == "resources") && g.Target != nil {
            return nil, fault.Invalid("--target", "conflicting_arguments", "event_local_target")
        }
        return service.Subscribe(ctx, client.SubscribeInput{Subject: subject, Source: v.Source, Target: g.Target, Destinations: v.Destination, Replay: v.Replay, Policy: c.policy})
    case "notify subscriptions":
        if err := boundedAsyncLimit(g.Notify.Subscriptions.Limit); err != nil {
            return nil, err
        }
        return service.ListSubscriptions(ctx, client.SubscriptionListInput{After: g.Notify.Subscriptions.After, Limit: g.Notify.Subscriptions.Limit})
    case "notify", "notify deliveries":
        if err := boundedAsyncLimit(g.Notify.Deliveries.Limit); err != nil {
            return nil, err
        }
        return service.Deliveries(ctx, g.Notify.Deliveries.Limit)
    case "notify serve":
        v := g.Notify.Serve
        if v.Interval <= 0 {
            return nil, fault.Invalid("--interval", "out_of_range", "notify_interval_invalid")
        }
        var emit func(client.Delivery) error
        if !v.Once {
            emit = func(delivery client.Delivery) error {
                if p.json {
                    return p.stream(deliveryStream{Type: "delivery", Delivery: delivery})
                }
                return printDelivery(p.out, delivery, p.locale)
            }
        }
        result, err := service.Serve(ctx, client.NotifyServeInput{Once: v.Once, Interval: v.Interval, Policy: c.policy}, emit)
        if v.Once {
            return result, err
        }
        kind := "end"
        if err != nil {
            kind = "error"
        }
        return notifyServeEnd{Type: kind, Result: result}, err
    }
    return nil, fault.New("BAD_CONFIG", "invalid_command", nil)
}

func (c *invocation) queue(ctx context.Context, path string) (any, error) {
    if c.app == nil || c.app.Queue == nil {
        return nil, asyncUnavailable("queue")
    }
    service, g := c.app.Queue, c.grammar
    switch path {
    case "queue", "queue list":
        if err := boundedAsyncLimit(g.Queue.List.Limit); err != nil {
            return nil, err
        }
        return service.List(ctx, client.QueueListInput{Target: g.Target, Limit: g.Queue.List.Limit, Policy: c.policy})
    case "queue enable":
        if g.Queue.Enable.Slots < 1 {
            return nil, fault.Invalid("--slots", "out_of_range", "queue_slots_invalid")
        }
        return service.Enable(ctx, client.QueueEnableInput{Target: g.Target, Slots: g.Queue.Enable.Slots, Policy: c.policy})
    case "queue submit":
        v := g.Queue.Submit
        args, err := commandArgs(v.Argv, false)
        if err != nil {
            return nil, err
        }
        if len(v.Argv) > 0 && len(args) == 0 {
            return nil, fault.Invalid("command", "missing_argument", "run_command")
        }
        return service.Submit(ctx, client.QueueSubmitInput{Target: g.Target, Args: args, Name: v.Name, Tags: v.Tag, Notify: v.Notify, DryRun: v.DryRun, Policy: c.policy}, c.progress)
    case "queue show":
        return service.Show(ctx, client.QueueIDInput{ID: g.Queue.Show.ID, Policy: c.policy})
    case "queue cancel":
        return service.Cancel(ctx, client.QueueIDInput{ID: g.Queue.Cancel.ID, Policy: c.policy})
    case "queue pause", "queue resume":
        return service.SetPaused(ctx, client.QueuePauseInput{Target: g.Target, Paused: path == "queue pause", Policy: c.policy})
    case "queue clean":
        v := g.Queue.Clean
        if !v.DryRun && !v.Yes && c.policy.Batch {
            return nil, fault.Invalid("--yes", "missing_argument", "queue_clean_confirmation_required")
        }
        preview, err := service.Clean(ctx, client.QueueCleanInput{IDs: v.IDs, DryRun: true, Policy: c.policy})
        if err != nil || v.DryRun {
            return queueCleanResult{DryRun: true, IDs: v.IDs, Result: preview}, err
        }
        if !v.Yes {
            if err := printQueueResult(c.output.err, preview, c.output.locale); err != nil {
                return preview, err
            }
            fmt.Fprintln(c.output.err, c.output.locale.Text("queue_clean_scope", map[string]any{"Value": strings.Join(v.IDs, ", ")}))
            yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: c.output.locale.Text("queue_clean_confirm"), Kind: interaction.ConfirmDefaultNo, AcceptLabel: c.output.locale.Text("yes"), RejectLabel: c.output.locale.Text("no")})
            if err != nil {
                return preview, err
            }
            if !yes {
                return preview, fault.New("CLEAN_CANCELED", "clean_canceled", nil)
            }
        }
        result, err := service.Clean(ctx, client.QueueCleanInput{IDs: v.IDs, Policy: c.policy})
        return queueCleanResult{IDs: v.IDs, Result: result}, err
    }
    return nil, fault.New("BAD_CONFIG", "invalid_command", nil)
}

type deliveryStream struct {
    Type     string          `json:"type"`
    Delivery client.Delivery `json:"delivery"`
}

type notifyServeEnd struct {
    Type   string                   `json:"type"`
    Result client.NotifyServeResult `json:"result"`
}

type queueCleanResult struct {
    DryRun bool         `json:"dry_run"`
    IDs    []string     `json:"request_ids"`
    Result queue.Result `json:"result"`
}

type destinationRemoval struct {
    Name    string `json:"name"`
    Removed bool   `json:"removed"`
}
