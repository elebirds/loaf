package cli

import (
    "context"
    "errors"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *invocation) uxISubscriptions(ctx context.Context) error {
    cursors, page, query := []string{""}, 0, ""
    for {
        result, err := c.app.Notify.ListSubscriptions(ctx, client.SubscriptionListInput{After: cursors[page], Limit: c.uxIPageSize()})
        if err != nil {
            return err
        }
        choices := []interaction.Choice{}
        for _, sub := range result.Subscriptions {
            if uxIMatches(query, sub.ID, sub.Subject.Kind, sub.Subject.ID, sub.Status, strings.Join(sub.Destinations, " ")) {
                choices = append(choices, interaction.Choice{Label: c.uxILabel(sub.Subject.Kind + ":" + sub.Subject.ID + " · " + c.uxIState(sub.Status)), Value: "item:" + sub.ID})
            }
        }
        summary := c.uxICoverage(len(result.Subscriptions), result.HasMore)
        if query != "" {
            summary += "\n" + c.output.locale.Text("ux_i_page_search", map[string]any{"Count": len(choices)})
        }
        if result.HasMore {
            choices = append(choices, c.uxIChoice("next"))
        }
        if page > 0 {
            choices = append(choices, c.uxIChoice("previous"))
        }
        for _, a := range []string{"subscribe", "search", "refresh", "back"} {
            choices = append(choices, c.uxIChoice(a))
        }
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_i_subscriptions"), summary, choices, "back")
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "next":
            cursors = append(cursors[:page+1], result.NextAfter)
            page++
        case "previous":
            page--
        case "refresh":
            cursors, page = []string{""}, 0
        case "search":
            query, err = c.uxIText(ctx, "ux_i_search_prompt")
        case "subscribe":
            var selected *client.SubscribeInput
            selected, err = c.uxISelectSource(ctx, true)
            if err == nil && selected != nil {
                _, err = c.uxISubscribe(ctx, *selected)
            }
        default:
            err = c.uxISubscription(ctx, strings.TrimPrefix(action, "item:"))
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return err
        }
    }
}

func (c *invocation) uxISourceRows(sub client.Subscription) []interaction.Row {
    rows := []interaction.Row{}
    for _, source := range sub.Sources {
        rows = append(rows, c.uxIRow("source", source.Source+" · "+source.Subject.Kind+":"+source.Subject.ID+" · "+source.ID))
        synced := c.output.locale.Text("ux_i_unknown")
        if source.LastSyncedAt != nil {
            synced = uxITime(*source.LastSyncedAt)
        }
        rows = append(rows, c.uxIRow("synced", synced))
        if source.SourceError != "" {
            at := "—"
            if source.SourceErrorAt != nil {
                at = uxITime(*source.SourceErrorAt)
            }
            rows = append(rows, c.uxIRow("source_failure", source.SourceError+" · "+at))
        }
    }
    return rows
}

func (c *invocation) uxISubscriptionDocument(ctx context.Context, sub client.Subscription) (interaction.Document, error) {
    doc := interaction.Document{Title: c.output.locale.Text("ux_i_subscription"), Rows: []interaction.Row{
        c.uxIRow("subscription_id", sub.ID), c.uxIRow("subject", sub.Subject.Kind+":"+sub.Subject.ID),
        c.uxIRow("state", c.uxIState(sub.Status)), c.uxIRow("created", uxITime(sub.CreatedAt)),
    }, Footer: c.output.locale.Text("ux_i_source_history")}
    doc.Rows = append(doc.Rows, c.uxISourceRows(sub)...)
    setup, ok := c.app.Notify.(client.NotificationSetupService)
    if !ok {
        return doc, asyncUnavailable("notify subscription show")
    }
    recipients, err := setup.SubscriptionRecipients(ctx, sub.ID)
    for _, d := range recipients {
        doc.Rows = append(doc.Rows, c.uxIRow("recipient", d.Name+" · "+d.Kind+" · "+d.SessionID))
    }
    return doc, err
}

func (c *invocation) uxISubscription(ctx context.Context, id string) error {
    control, err := c.uxIControl()
    if err != nil {
        return err
    }
    for {
        sub, err := control.ShowSubscription(ctx, id)
        if err != nil {
            return err
        }
        actions := []string{"details"}
        switch sub.Status {
        case "enabled":
            actions = append(actions, "disable", "remove")
        case "disabled":
            actions = append(actions, "enable", "remove")
        }
        actions = append(actions, "refresh", "back")
        action, err := c.uxIMenu(ctx, "ux_i_subscription", c.uxILabel(sub.Subject.Kind+":"+sub.Subject.ID+" · "+sub.Status), actions...)
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "details":
            doc, cause := c.uxISubscriptionDocument(ctx, sub)
            if cause != nil {
                doc.Body = c.output.error(cause).Message
            }
            err = c.uxShowDocument(ctx, doc)
        case "disable", "enable", "remove":
            yes, e := c.uxIConfirm(ctx, "ux_i_"+action+"_confirm", c.output.locale.Text("ux_i_lifecycle_scope"))
            if e != nil {
                return e
            }
            if !yes {
                continue
            }
            state := map[string]string{"disable": "disabled", "enable": "enabled", "remove": "removed"}[action]
            result, cause := control.ChangeSubscription(ctx, id, state)
            err = c.uxShowResult(ctx, result, cause)
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return err
        }
    }
}

// uxISubscribeRun is the task-detail seam: a saved project-scoped identity,
// never the current default machine. Replay keeps the direct command's default.
func (c *invocation) uxISubscribeRun(ctx context.Context, id string) (any, error) {
    subject := event.Subject{Kind: "run", ID: id}
    if subject.Validate() != nil {
        return nil, fault.Invalid("run-id", "invalid_value", "event_subject_invalid")
    }
    return c.uxISubscribe(ctx, client.SubscribeInput{Source: "project", Subject: subject, Replay: true})
}

func (c *invocation) uxISubscribe(ctx context.Context, in client.SubscribeInput) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    if c.app == nil || c.app.Notify == nil {
        return nil, asyncUnavailable("notify subscribe")
    }
    if in.Subject.Validate() != nil {
        return nil, fault.Invalid("subject", "invalid_value", "event_subject_invalid")
    }
    rows, err := c.app.Notify.Destinations(ctx)
    if err != nil {
        return nil, err
    }
    if len(rows) == 0 {
        return nil, c.uxShowDocument(ctx, interaction.Document{Title: c.output.locale.Text("ux_i_no_destinations"), Body: c.output.locale.Text("ux_i_add_first")})
    }
    choices := []interaction.Choice{}
    for _, d := range rows {
        choices = append(choices, interaction.Choice{Label: c.uxILabel(d.Name + " · " + d.Kind + " · " + d.SessionID), Value: d.Name})
    }
    destinations := &interaction.Field{Title: c.output.locale.Text("ux_i_destinations"), Choices: choices, Multiple: true, Values: in.Destinations,
        ValidateValues: func(values []string) error {
            if len(values) < 1 || len(values) > 32 {
                return errors.New(c.output.locale.Text("notify_destination_invalid"))
            }
            return nil
        }}
    replay := "no"
    if in.Replay {
        replay = "yes"
    }
    replayField := &interaction.Field{Title: c.output.locale.Text("ux_i_replay"), Choices: []interaction.Choice{{Label: c.output.locale.Text("ux_i_replay_yes"), Value: "yes"}, {Label: c.output.locale.Text("ux_i_replay_no"), Value: "no"}}, Value: replay}
    for {
        if err = c.input.Page(ctx, c.output.locale.Text("ux_i_subscribe"), destinations, replayField); err != nil {
            return nil, err
        }
        // Plain multi-select has no Huh validator; keep the same local check.
        if err = destinations.ValidateValues(destinations.Values); err != nil {
            if err = c.uxIHandle(ctx, err); err != nil {
                return nil, err
            }
            continue
        }
        in.Destinations, in.Replay, in.Policy = destinations.Values, replayField.Value == "yes", c.policy
        doc := interaction.Document{Title: c.output.locale.Text("ux_i_review"), Rows: []interaction.Row{
            c.uxIRow("subject", in.Subject.Kind+":"+in.Subject.ID), c.uxIRow("source", in.Source),
            c.uxIRow("replay", replayField.Choices[1].Label),
        }, Footer: c.output.locale.Text("ux_i_subscription_scope")}
        if in.Replay {
            doc.Rows[2].Value = replayField.Choices[0].Label
        }
        if in.Target != nil {
            doc.Rows = append(doc.Rows, c.uxIRow("target", *in.Target))
        }
        for _, name := range in.Destinations {
            for _, d := range rows {
                if name == d.Name {
                    doc.Rows = append(doc.Rows, c.uxIRow("recipient", d.Name+" · "+d.Kind+" · "+d.SessionID))
                }
            }
        }
        if err = c.uxShowDocument(ctx, doc); err != nil {
            return nil, err
        }
        action, err := c.uxIMenu(ctx, "ux_i_review", "", "save", "edit", "back")
        if err != nil || action == "back" {
            return nil, err
        }
        if action == "edit" {
            continue
        }
        var result client.Subscription
        // Replay=false may read the remote source; release the form owner first.
        err = c.input.WithNative(ctx, nil, func() error { var e error; result, e = c.app.Notify.Subscribe(ctx, in); return e })
        return nil, c.uxShowResult(ctx, result, err)
    }
}
