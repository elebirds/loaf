package cli

import (
    "fmt"
    "io"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/queue"
)

func printAsync(out io.Writer, data any, l *i18n.Localizer) (bool, error) {
    switch r := data.(type) {
    case event.Page:
        for _, e := range r.Events {
            if _, err := fmt.Fprintf(out, "%s  %s  %s:%s  %s\n", e.OccurredAt.Format(time.RFC3339), eventTypeText(e.Type, l), e.Subject.Kind, e.Subject.ID, e.ID); err != nil {
                return true, err
            }
        }
        if len(r.Events) == 0 {
            _, err := fmt.Fprintln(out, l.Text("events_empty"))
            return true, err
        }
        if r.HasMore {
            _, err := fmt.Fprintln(out, l.Text("events_more", map[string]any{"Value": r.NextCursor}))
            return true, err
        }
        return true, nil
    case client.DestinationRecord:
        return true, printDestination(out, r, l)
    case []client.DestinationRecord:
        if len(r) == 0 {
            _, err := fmt.Fprintln(out, l.Text("notify_destinations_empty"))
            return true, err
        }
        for _, d := range r {
            if err := printDestination(out, d, l); err != nil {
                return true, err
            }
        }
        return true, nil
    case client.Subscription:
        return true, printSubscription(out, r, l)
    case []client.Subscription:
        if len(r) == 0 {
            _, err := fmt.Fprintln(out, l.Text("notify_subscriptions_empty"))
            return true, err
        }
        for _, s := range r {
            if err := printSubscription(out, s, l); err != nil {
                return true, err
            }
        }
        return true, nil
    case []client.Delivery:
        if len(r) == 0 {
            _, err := fmt.Fprintln(out, l.Text("notify_deliveries_empty"))
            return true, err
        }
        for _, d := range r {
            if err := printDelivery(out, d, l); err != nil {
                return true, err
            }
        }
        return true, nil
    case client.NotifyServeResult:
        return true, printNotifySummary(out, r, l)
    case notifyServeEnd:
        return true, printNotifySummary(out, r.Result, l)
    case queue.Result:
        return true, printQueueResult(out, r, l)
    case queueCleanResult:
        if err := printQueueResult(out, r.Result, l); err != nil {
            return true, err
        }
        if r.DryRun {
            _, err := fmt.Fprintln(out, l.Text("queue_clean_scope", map[string]any{"Value": strings.Join(r.IDs, ", ")}))
            return true, err
        }
        return true, nil
    case client.QueueSubmitResult:
        if r.Preview != nil {
            printPreview(out, *r.Preview, l)
            return true, nil
        }
        if r.Request != nil {
            if err := printQueueRequest(out, *r.Request, l); err != nil {
                return true, err
            }
            if _, err := fmt.Fprintln(out, l.Text("queue_next", map[string]any{"ID": r.Request.ID})); err != nil {
                return true, err
            }
        }
        if r.Run != nil {
            printRun(out, *r.Run, l)
        }
        return true, nil
    }
    return false, nil
}

func eventTypeText(kind string, l *i18n.Localizer) string {
    switch kind {
    case "run.accepted", "run.succeeded", "run.failed", "run.stopped", "queue.enqueued", "queue.dispatched", "queue.canceled", "queue.attention_required", "resource.acquired", "resource.ready", "resource.started", "resource.stopped", "resource.attention_required", "attention.required":
        return l.Text("event_type_" + strings.ReplaceAll(kind, ".", "_"))
    }
    return kind
}

func printDestination(out io.Writer, r client.DestinationRecord, l *i18n.Localizer) error {
    _, err := fmt.Fprintf(out, "%s  %s  %s\n", r.Name, r.Kind, r.SessionID)
    return err
}

func printSubscription(out io.Writer, r client.Subscription, l *i18n.Localizer) error {
    _, err := fmt.Fprintf(out, "%s  %s:%s  %s  %s\n", r.ID, r.Subject.Kind, r.Subject.ID, r.Source, strings.Join(r.Destinations, ", "))
    if err != nil {
        return err
    }
    if r.LastSyncedAt != nil {
        if _, err = fmt.Fprintln(out, "  "+l.Text("notify_source_synced", map[string]any{"Value": r.LastSyncedAt.Format(time.RFC3339)})); err != nil {
            return err
        }
    }
    if r.SourceError != "" {
        id, params := "notify_source_error", map[string]any{"Code": r.SourceError}
        if r.SourceErrorAt != nil {
            id = "notify_source_error_at"
            params["Value"] = r.SourceErrorAt.Format(time.RFC3339)
        }
        _, err = fmt.Fprintln(out, "  "+l.Text(id, params))
    }
    return err
}

func printDelivery(out io.Writer, r client.Delivery, l *i18n.Localizer) error {
    state := r.State
    if state == "" {
        state = "pending"
    }
    switch state {
    case "pending", "in_flight", "accepted", "processed", "unconfirmed", "rejected":
        state = l.Text("delivery_dispatch_" + state)
    }
    _, err := fmt.Fprintf(out, "%s  %s  %s  %s", r.ID, r.EventID, r.Destination, state)
    if err != nil {
        return err
    }
    if r.ErrorCode != "" {
        if _, err = fmt.Fprintf(out, "  %s", r.ErrorCode); err != nil {
            return err
        }
    }
    if _, err = fmt.Fprintln(out); err != nil {
        return err
    }
    if r.Receipt.State != "" {
        evidence := r.Receipt.State
        switch evidence {
        case "pending", "written", "accepted", "processed", "unconfirmed", "rejected":
            evidence = l.Text("delivery_state_" + evidence)
        }
        if _, err = fmt.Fprintln(out, "  "+l.Text("delivery_evidence", map[string]any{"Value": evidence})); err != nil {
            return err
        }
    }
    if r.NextAttemptAt != nil {
        _, err = fmt.Fprintln(out, "  "+l.Text("delivery_next_attempt", map[string]any{"Value": r.NextAttemptAt.Format(time.RFC3339)}))
    }
    return err
}

func printNotifySummary(out io.Writer, r client.NotifyServeResult, l *i18n.Localizer) error {
    _, err := fmt.Fprintln(out, l.Text("notify_summary", map[string]any{"Count": r.Received, "Total": r.Attempted, "Matched": r.Accepted, "Value": r.Unconfirmed, "Bytes": r.Pending}))
    return err
}

func printQueueRequest(out io.Writer, r queue.Request, l *i18n.Localizer) error {
    state := r.State
    switch state {
    case "preparing", "queued", "dispatching", "submitted", "running", "completed", "canceled", "unconfirmed", "dispatch_unconfirmed":
        state = l.Text("queue_state_" + state)
    }
    _, err := fmt.Fprintf(out, "%s  %s  %s", r.ID, state, r.RunID)
    if err != nil {
        return err
    }
    if r.Name != "" {
        if _, err = fmt.Fprintf(out, "  %q", r.Name); err != nil {
            return err
        }
    }
    if _, err = fmt.Fprintln(out); err != nil {
        return err
    }
    if r.ReasonCode != "" {
        _, err = fmt.Fprintln(out, "  "+r.ReasonCode)
    }
    return err
}

func printQueueResult(out io.Writer, r queue.Result, l *i18n.Localizer) error {
    if r.StateError != "" {
        if _, err := fmt.Fprintln(out, l.Text("queue_state_save_failed", map[string]any{"Code": r.StateError})); err != nil {
            return err
        }
    }
    if r.Status != nil {
        state := "queue_active"
        if r.Status.Paused {
            state = "queue_paused"
        }
        if _, err := fmt.Fprintln(out, l.Text("queue_summary", map[string]any{"Target": r.Status.TargetID, "Value": l.Text(state), "Count": r.Status.Reserved, "Total": r.Status.Slots, "Matched": r.Status.Queued})); err != nil {
            return err
        }
        if _, err := fmt.Fprintln(out, l.Text("queue_capacity_scope")); err != nil {
            return err
        }
        if !r.Status.CoordinatorRunning {
            if _, err := fmt.Fprintln(out, l.Text("queue_coordinator_inactive")); err != nil {
                return err
            }
        }
    }
    if r.Request != nil {
        if err := printQueueRequest(out, *r.Request, l); err != nil {
            return err
        }
    }
    for _, request := range r.Requests {
        if err := printQueueRequest(out, request, l); err != nil {
            return err
        }
    }
    if len(r.Cleaned) > 0 {
        _, err := fmt.Fprintln(out, l.Text("queue_clean_done", map[string]any{"Value": strings.Join(r.Cleaned, ", ")}))
        return err
    }
    if r.Request == nil && len(r.Requests) == 0 && r.Status != nil {
        _, err := fmt.Fprintln(out, l.Text("queue_empty"))
        return err
    }
    return nil
}
