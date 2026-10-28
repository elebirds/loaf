package cli

import (
    "context"
    "fmt"
    "strings"

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
    switch path {
    case "notify destination", "notify destination list":
        return service.Destinations(ctx)
    case "notify destination add":
        v := g.Notify.Destination.Add
        destination := agentdelivery.Destination{Kind: v.Kind, Endpoint: v.Endpoint, SessionID: v.Session, CWD: v.CWD}
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
        return service.Subscriptions(ctx)
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
