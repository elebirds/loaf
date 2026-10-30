package cli

import (
    "context"
    "errors"
    "strings"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *invocation) uxINotify(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    if c.app == nil || c.app.Notify == nil {
        return nil, asyncUnavailable("notify")
    }
    for {
        action, err := c.uxIMenu(ctx, "ux_i_notify", "", "destinations", "subscriptions", "deliveries", "attention", "events", "prune", "back")
        if errors.Is(err, interaction.ErrCanceled) {
            return nil, nil
        }
        if err != nil || action == "back" {
            return nil, err
        }
        switch action {
        case "destinations":
            err = c.uxIDestinations(ctx)
        case "subscriptions":
            err = c.uxISubscriptions(ctx)
        case "deliveries", "attention":
            err = c.uxIDeliveries(ctx, action == "attention")
        case "events":
            _, err = c.uxIEvents(ctx)
        case "prune":
            err = c.uxIPrune(ctx)
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return nil, err
        }
    }
}

func (c *invocation) uxIMenu(ctx context.Context, title, summary string, actions ...string) (string, error) {
    choices := make([]interaction.Choice, 0, len(actions))
    for _, action := range actions {
        choices = append(choices, c.uxIChoice(action))
    }
    return c.input.SelectWithSummary(ctx, c.output.locale.Text(title), summary, choices, "back")
}

func (c *invocation) uxIChoice(action string) interaction.Choice {
    return interaction.Choice{Label: c.output.locale.Text("ux_i_" + action), Value: action}
}

func (c *invocation) uxIHandle(ctx context.Context, err error) error {
    if ctx.Err() != nil {
        return ctx.Err()
    }
    if err == nil || errors.Is(err, interaction.ErrCanceled) {
        return nil
    }
    return c.uxShowResult(ctx, nil, err)
}

func (c *invocation) uxIControl() (client.NotificationControlService, error) {
    if c.app == nil {
        return nil, asyncUnavailable("notify")
    }
    control, ok := c.app.Notify.(client.NotificationControlService)
    if !ok {
        return nil, asyncUnavailable("notify delivery show")
    }
    return control, nil
}

func (c *invocation) uxIConfirm(ctx context.Context, title, description string) (bool, error) {
    return c.input.Confirm(ctx, interaction.Confirmation{Title: c.output.locale.Text(title), Description: description,
        Kind: interaction.ConfirmDefaultNo, AcceptLabel: c.output.locale.Text("yes"), RejectLabel: c.output.locale.Text("no")})
}

func (c *invocation) uxIText(ctx context.Context, title string) (string, error) {
    return c.input.Text(ctx, interaction.TextPrompt{Title: c.output.locale.Text(title), MaxBytes: 256})
}

func (c *invocation) uxILabel(value string) string {
    // Full identities are retained as values and shown in the paged detail.
    return ansi.Truncate(strings.Map(func(r rune) rune {
        if r < 32 || r == 127 {
            return -1
        }
        return r
    }, value), max(24, interaction.Width(c.options.Err)-12), "…")
}

func (c *invocation) uxIRow(id, value string) interaction.Row {
    return interaction.Row{Label: c.output.locale.Text("ux_i_" + id), Value: value}
}

func (c *invocation) uxIState(value string) string {
    switch value {
    case "enabled", "disabled", "removed":
        return c.output.locale.Text("notify_status_"+value) + " (" + value + ")"
    case "pending", "in_flight", "accepted", "processed", "unconfirmed", "rejected", "resolved":
        return c.output.locale.Text("delivery_dispatch_"+value) + " (" + value + ")"
    case "written":
        return c.output.locale.Text("delivery_state_written") + " (written)"
    default:
        return value
    }
}

func uxITime(value time.Time) string {
    if value.IsZero() {
        return "—"
    }
    return value.Format(time.RFC3339)
}

func uxIMatches(query string, values ...string) bool {
    return strings.Contains(strings.ToLower(strings.Join(values, " ")), strings.ToLower(query))
}

func (c *invocation) uxIPageSize() int { return max(1, min(5, interaction.Height(c.options.Err)-15)) }

func (c *invocation) uxICoverage(count int, more bool) string {
    key := "ux_i_coverage"
    if more {
        key = "ux_i_coverage_more"
    }
    return c.output.locale.Text(key, map[string]any{"Count": count})
}

func (c *invocation) uxIPrune(ctx context.Context) error {
    control, err := c.uxIControl()
    if err != nil {
        return err
    }
    value, err := c.uxIText(ctx, "ux_i_cutoff")
    if err != nil || value == "" {
        return err
    }
    before, err := time.Parse(time.RFC3339Nano, value)
    if err != nil {
        return fault.Invalid("--before", "invalid_value", "notify_before_invalid")
    }
    preview, err := control.Prune(ctx, client.PruneInput{Before: before, DryRun: true})
    if err != nil {
        return err
    }
    if err = c.uxShowResult(ctx, preview, nil); err != nil {
        return err
    }
    yes, err := c.uxIConfirm(ctx, "ux_i_prune_confirm", c.output.locale.Text("ux_i_prune_scope"))
    if err != nil || !yes {
        return err
    }
    result, err := control.Prune(ctx, client.PruneInput{Before: before})
    return c.uxShowResult(ctx, result, err)
}
