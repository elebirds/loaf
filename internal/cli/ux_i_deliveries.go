package cli

import (
    "context"
    "fmt"
    "strconv"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
)

func (c *invocation) uxIDeliveries(ctx context.Context, attention bool) error {
    limit, offset, query := 100, 0, ""
    for {
        rows, err := c.app.Notify.Deliveries(ctx, limit)
        if err != nil {
            return err
        }
        filtered := []client.Delivery{}
        for _, d := range rows {
            if attention && (d.State != "unconfirmed" || d.Resolution != nil) {
                continue
            }
            if uxIMatches(query, d.ID, d.Destination, d.SubscriptionID, d.State, d.Receipt.State, d.ErrorCode) {
                filtered = append(filtered, d)
            }
        }
        if offset >= len(filtered) {
            offset = 0
        }
        end := min(len(filtered), offset+c.uxIPageSize())
        choices := []interaction.Choice{}
        for _, d := range filtered[offset:end] {
            choices = append(choices, interaction.Choice{Label: c.uxILabel(d.Destination + " · " + c.uxIState(d.State) + " · " + uxITime(d.UpdatedAt) + " · " + d.ID), Value: "item:" + d.ID})
        }
        if end < len(filtered) {
            choices = append(choices, c.uxIChoice("next"))
        }
        if offset > 0 {
            choices = append(choices, c.uxIChoice("previous"))
        }
        for _, a := range []string{"search", "limit", "refresh", "back"} {
            choices = append(choices, c.uxIChoice(a))
        }
        summary := c.output.locale.Text("ux_i_delivery_coverage", map[string]any{"Count": len(filtered), "Total": len(rows), "Value": limit})
        title := "ux_i_deliveries"
        if attention {
            title = "ux_i_attention"
        }
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text(title), summary, choices, "back")
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "next":
            offset = end
        case "previous":
            offset = max(0, offset-c.uxIPageSize())
        case "search":
            query, err = c.uxIText(ctx, "ux_i_delivery_search")
            offset = 0
        case "limit":
            var value string
            value, err = c.uxIText(ctx, "ux_i_limit_prompt")
            if err == nil {
                var n int
                n, err = strconv.Atoi(value)
                if err != nil {
                    err = boundedAsyncLimit(0)
                } else {
                    err = boundedAsyncLimit(n)
                }
                if err == nil {
                    limit, offset = n, 0
                }
            }
        case "refresh":
        default:
            err = c.uxIDelivery(ctx, strings.TrimPrefix(action, "item:"))
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return err
        }
    }
}

func (c *invocation) uxIDeliveryDocument(d client.DeliveryDetail) interaction.Document {
    l := c.output.locale
    observed := func(yes bool) string {
        if yes {
            return l.Text("ux_i_recorded")
        }
        return l.Text("ux_i_unknown")
    }
    r := d.Delivery.Receipt
    doc := interaction.Document{Title: l.Text("ux_i_evidence"), Rows: []interaction.Row{
        c.uxIRow("delivery_id", d.Delivery.ID), c.uxIRow("recipient", d.Recipient.Name+" · "+d.Recipient.Kind+" · "+d.Recipient.SessionID),
        c.uxIRow("state", c.uxIState(d.Delivery.State)), c.uxIRow("updated", uxITime(d.Delivery.UpdatedAt)),
        c.uxIRow("source_fact", observed(d.Event.ID != "")+" · "+d.Event.ID+" · "+uxITime(d.Event.OccurredAt)),
        c.uxIRow("local_receipt", observed(d.Delivery.EventID != "")+" · "+d.Delivery.EventID),
        c.uxIRow("transport", observed(r.State == "written" || r.Accepted())),
        c.uxIRow("acceptance", observed(r.Accepted())), c.uxIRow("processing", observed(r.State == "processed")),
        c.uxIRow("receipt", c.uxIState(r.State)), c.uxIRow("operation_id", r.OperationID), c.uxIRow("code", d.Delivery.ErrorCode),
        c.uxIRow("reason", r.ReasonCode), c.uxIRow("subscription_id", d.Delivery.SubscriptionID), c.uxIRow("parent_id", d.Delivery.ParentID),
    }, Footer: l.Text("ux_i_evidence_hint")}
    if d.Delivery.NextAttemptAt != nil {
        doc.Rows = append(doc.Rows, c.uxIRow("next_attempt", uxITime(*d.Delivery.NextAttemptAt)))
    }
    if d.Delivery.Resolution != nil {
        v := d.Delivery.Resolution
        doc.Rows = append(doc.Rows, c.uxIRow("resolution", v.Kind+" · "+uxITime(v.At)), c.uxIRow("successor_id", v.SuccessorID))
    }
    for _, a := range d.Attempts {
        value := fmt.Sprintf("%d · %s → %s · %s · %s", a.Number, uxITime(a.StartedAt), l.Text("ux_i_unknown"), c.uxIState(a.Receipt.State), a.ErrorCode)
        if a.CompletedAt != nil {
            value = fmt.Sprintf("%d · %s → %s · %s · %s", a.Number, uxITime(a.StartedAt), uxITime(*a.CompletedAt), c.uxIState(a.Receipt.State), a.ErrorCode)
        }
        doc.Rows = append(doc.Rows, c.uxIRow("attempt", value))
    }
    return doc
}

func uxIDeliveryActions(d client.DeliveryDetail, sub client.Subscription) []string {
    actions := []string{"evidence", "subscription"}
    if d.Delivery.State == "unconfirmed" && d.Recipient.Kind == "claude-channel" {
        actions = append(actions, "reconcile")
    }
    if sub.Status == "enabled" {
        actions = append(actions, "disable")
    }
    if d.Delivery.Resolution == nil && (d.Delivery.State == "unconfirmed" || d.Delivery.State == "rejected") {
        actions = append(actions, "confirmed", "abandoned")
        if sub.Status == "enabled" {
            actions = append(actions, "resend")
        }
    }
    if d.Delivery.Resolution != nil && d.Delivery.Resolution.SuccessorID != "" {
        actions = append(actions, "successor")
    }
    return append(actions, "refresh", "back")
}

func (c *invocation) uxIDelivery(ctx context.Context, id string) error {
    control, err := c.uxIControl()
    if err != nil {
        return err
    }
    for {
        detail, err := control.ShowDelivery(ctx, id)
        if err != nil {
            return err
        }
        sub, subErr := control.ShowSubscription(ctx, detail.Delivery.SubscriptionID)
        if subErr != nil {
            if err = c.uxIHandle(ctx, subErr); err != nil {
                return err
            }
        }
        summary := c.uxILabel(detail.Recipient.Name + " · " + detail.Recipient.Kind + " · " + c.uxIState(detail.Delivery.State))
        action, err := c.uxIMenu(ctx, "ux_i_delivery", summary, uxIDeliveryActions(detail, sub)...)
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "evidence":
            err = c.uxShowDocument(ctx, c.uxIDeliveryDocument(detail))
        case "subscription":
            err = c.uxISubscription(ctx, detail.Delivery.SubscriptionID)
        case "reconcile":
            result, cause := control.ReconcileDelivery(ctx, id)
            err = c.uxShowResult(ctx, result, cause)
        case "disable":
            yes, e := c.uxIConfirm(ctx, "ux_i_disable_confirm", c.output.locale.Text("ux_i_lifecycle_scope"))
            if e != nil {
                return e
            }
            if !yes {
                continue
            }
            result, cause := control.ChangeSubscription(ctx, sub.ID, "disabled")
            err = c.uxShowResult(ctx, result, cause)
        case "confirmed", "abandoned":
            if err = c.uxShowDocument(ctx, c.uxIDeliveryDocument(detail)); err != nil {
                return err
            }
            yes, e := c.uxIConfirm(ctx, "ux_i_resolve_"+action, c.output.locale.Text("ux_i_platform_evidence"))
            if e != nil {
                return e
            }
            if !yes {
                continue
            }
            result, cause := control.ResolveDelivery(ctx, id, action)
            err = c.uxShowResult(ctx, result, cause)
        case "resend":
            if err = c.uxShowDocument(ctx, c.uxIDeliveryDocument(detail)); err != nil {
                return err
            }
            // The UI equivalent of --allow-duplicate is a separate explicit
            // decision; merely visiting this page never authorizes a successor.
            yes, e := c.uxIConfirm(ctx, "ux_i_allow_duplicate", c.output.locale.Text("ux_i_duplicate_risk"))
            if e != nil {
                return e
            }
            if !yes {
                continue
            }
            yes, e = c.uxIConfirm(ctx, "ux_i_resend_confirm", c.uxILabel(detail.Recipient.Name+" · "+detail.Recipient.SessionID))
            if e != nil {
                return e
            }
            if !yes {
                continue
            }
            result, cause := control.ResendDelivery(ctx, id)
            err = c.uxShowResult(ctx, result, cause)
        case "successor":
            id = detail.Delivery.Resolution.SuccessorID
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return err
        }
    }
}
