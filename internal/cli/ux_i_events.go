package cli

import (
    "context"
    "fmt"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
)

func (c *invocation) uxIEvents(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    if c.app == nil || c.app.Events == nil {
        return nil, asyncUnavailable("events list")
    }
    _, err := c.uxISelectSource(ctx, false)
    return nil, err
}

func (c *invocation) uxISelectSource(ctx context.Context, pick bool) (*client.SubscribeInput, error) {
    if c.app == nil || c.app.Events == nil {
        return nil, asyncUnavailable("events list")
    }
    for {
        actions := []string{"local", "resources", "project", "queue", "source_health", "back"}
        if pick {
            actions = []string{"resources", "project", "queue", "back"}
        }
        source, err := c.uxIMenu(ctx, "ux_i_events", "", actions...)
        if err != nil || source == "back" {
            return nil, err
        }
        if source == "source_health" {
            if err = c.uxIHandle(ctx, c.uxISourceHealth(ctx)); err != nil {
                return nil, err
            }
            continue
        }
        in := client.EventListInput{Source: source, Limit: c.uxIPageSize(), Policy: c.policy}
        if source == "project" || source == "queue" {
            target, e := c.uxIEventTarget(ctx)
            if e != nil {
                if err = c.uxIHandle(ctx, e); err != nil {
                    return nil, err
                }
                continue
            }
            if target == "" {
                continue
            }
            in.Target = &target
        }
        selected, err := c.uxIEventPages(ctx, in, pick)
        if selected != nil {
            return selected, err
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return nil, err
        }
    }
}

func (c *invocation) uxIEventTarget(ctx context.Context) (string, error) {
    if c.app.Machines == nil {
        return "", asyncUnavailable("machine list")
    }
    result, err := c.app.Machines.Targets(ctx, "")
    if err != nil {
        return "", err
    }
    choices := []interaction.Choice{}
    for _, target := range result.Targets {
        choices = append(choices, interaction.Choice{Label: c.uxILabel(target.Name), Value: "item:" + target.Name})
    }
    choices = append(choices, c.uxIChoice("back"))
    action, err := c.input.Select(ctx, c.output.locale.Text("ux_i_target"), choices, "back")
    if action == "back" {
        return "", err
    }
    return strings.TrimPrefix(action, "item:"), err
}

func (c *invocation) uxIEventPages(ctx context.Context, in client.EventListInput, pick bool) (*client.SubscribeInput, error) {
    cursors, position, query := []string{in.Cursor}, 0, ""
    originalTarget, reload := in.Target, true
    var page event.Page
    var cause error
    for {
        in.Cursor = cursors[position]
        if reload {
            cause = c.input.WithNative(ctx, nil, func() error { var e error; page, e = c.app.Events.List(ctx, in); return e })
            reload = false
        }
        if ctx.Err() != nil {
            return nil, ctx.Err()
        }
        choices := []interaction.Choice{}
        for i, e := range page.Events {
            if uxIMatches(query, e.Subject.Kind, e.Subject.ID, e.Type) {
                choices = append(choices, interaction.Choice{Label: c.uxILabel(e.Subject.Kind + ":" + e.Subject.ID + " · " + eventTypeText(e.Type, c.output.locale)), Value: fmt.Sprintf("item:%d", i)})
            }
        }
        summary := c.output.locale.Text("ux_i_"+in.Source) + " · " + c.uxICoverage(len(page.Events), page.HasMore)
        if in.Target != nil {
            summary += " · " + c.uxILabel(*in.Target)
        }
        if query != "" {
            summary += "\n" + c.output.locale.Text("ux_i_page_search", map[string]any{"Count": len(choices)})
        }
        if in.Subject != nil {
            summary += "\n" + c.uxILabel(in.Subject.Kind+":"+in.Subject.ID)
        }
        if cause != nil {
            view := c.output.error(cause)
            summary += "\n" + view.Code + ": " + view.Message
        }
        // A failed page may still contain verified facts. Keep them visible but
        // do not advance a cursor through unconfirmed coverage.
        if cause == nil && page.HasMore && page.NextCursor != "" {
            choices = append(choices, c.uxIChoice("next"))
        }
        if position > 0 {
            choices = append(choices, c.uxIChoice("previous"))
        }
        if in.Subject != nil {
            choices = append(choices, c.uxIChoice("clear_filter"))
        }
        for _, a := range []string{"search", "coverage", "refresh", "back"} {
            choices = append(choices, c.uxIChoice(a))
        }
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_i_events"), summary, choices, "back")
        if err != nil || action == "back" {
            return nil, err
        }
        switch action {
        case "next":
            cursors = append(cursors[:position+1], page.NextCursor)
            position++
            reload = true
        case "previous":
            position--
            reload = true
        case "clear_filter":
            in.Subject, cursors, position = nil, []string{""}, 0
            in.Target, reload = originalTarget, true
        case "refresh":
            reload = true
        case "search":
            query, err = c.uxIText(ctx, "ux_i_search_prompt")
        case "coverage":
            doc := interaction.Document{Title: c.output.locale.Text("ux_i_coverage_title"), Rows: []interaction.Row{
                c.uxIRow("source", in.Source+" · "+page.SourceID), c.uxIRow("cursor", in.Cursor), c.uxIRow("next_cursor", page.NextCursor),
            }, Body: c.uxICoverage(len(page.Events), page.HasMore), Footer: c.output.locale.Text("ux_i_event_coverage_hint")}
            if cause != nil {
                view := c.output.error(cause)
                doc.Body += "\n" + view.Code + ": " + view.Message + "\n" + view.Hint
            }
            err = c.uxShowDocument(ctx, doc)
        default:
            for i, e := range page.Events {
                if action == fmt.Sprintf("item:%d", i) {
                    action, err = c.uxIEvent(ctx, e, in.Source)
                    if err != nil {
                        break
                    }
                    if action == "filter" {
                        subject := e.Subject
                        in.Subject = &subject
                        // A run subject resolves its saved binding, not the browsing
                        // target. Queue filtering remains scoped to the chosen host.
                        if in.Source == "project" && subject.Kind == "run" {
                            in.Target = nil
                        }
                        cursors, position = []string{""}, 0
                        reload = true
                    }
                    if action == "subscribe" {
                        selected := client.SubscribeInput{Subject: e.Subject, Source: in.Source, Target: in.Target, Replay: true, Policy: c.policy}
                        if selected.Subject.Kind == "run" {
                            selected.Source, selected.Target = "project", nil
                        }
                        if selected.Subject.Kind == "resource" || selected.Subject.Kind == "resource_operation" {
                            selected.Source, selected.Target = "resources", nil
                        }
                        if pick {
                            return &selected, nil
                        }
                        _, err = c.uxISubscribe(ctx, selected)
                    }
                    break
                }
            }
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return nil, err
        }
    }
}

func (c *invocation) uxIEvent(ctx context.Context, e event.Envelope, source string) (string, error) {
    for {
        actions := []string{"details", "filter"}
        // Local queue events do not identify a frozen queue transport by alias.
        // Select that verified queue source instead of using today's default.
        if c.app.Notify != nil && !(source == "local" && e.Subject.Kind == "queue_request") {
            actions = append(actions, "subscribe")
        }
        actions = append(actions, "back")
        action, err := c.uxIMenu(ctx, "ux_i_event", c.uxILabel(e.Subject.Kind+":"+e.Subject.ID), actions...)
        if err != nil || action != "details" {
            return action, err
        }
        doc := interaction.Document{Title: eventTypeText(e.Type, c.output.locale), Rows: []interaction.Row{
            c.uxIRow("subject", e.Subject.Kind+":"+e.Subject.ID), c.uxIRow("occurred", uxITime(e.OccurredAt)),
            c.uxIRow("event_id", e.ID), c.uxIRow("source", e.SourceID), c.uxIRow("state", e.Data.Status),
            c.uxIRow("code", e.Data.Code),
        }, Body: c.output.locale.Text("ux_i_fact_hint")}
        if err = c.uxShowDocument(ctx, doc); err != nil {
            return "", err
        }
    }
}

func (c *invocation) uxISourceHealth(ctx context.Context) error {
    if c.app.Notify == nil {
        return asyncUnavailable("notify subscriptions")
    }
    after := ""
    for {
        page, err := c.app.Notify.ListSubscriptions(ctx, client.SubscriptionListInput{After: after, Limit: c.uxIPageSize()})
        if err != nil {
            return err
        }
        doc := interaction.Document{Title: c.output.locale.Text("ux_i_source_health"), Footer: c.output.locale.Text("ux_i_source_history"), Body: c.uxICoverage(len(page.Subscriptions), page.HasMore)}
        for _, sub := range page.Subscriptions {
            doc.Rows = append(doc.Rows, c.uxIRow("subscription_id", sub.ID+" · "+c.uxIState(sub.Status)))
            doc.Rows = append(doc.Rows, c.uxISourceRows(sub)...)
        }
        if err = c.uxShowDocument(ctx, doc); err != nil {
            return err
        }
        if !page.HasMore {
            return nil
        }
        action, err := c.uxIMenu(ctx, "ux_i_source_health", "", "next", "back")
        if err != nil || action == "back" {
            return err
        }
        after = page.NextAfter
    }
}
