package cli

import (
    "errors"
    "fmt"
    "io"
    "strconv"
    "strings"
    "time"
    "unicode"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
)

func (c *invocation) uxHChoice(action string) interaction.Choice {
    return interaction.Choice{Label: c.output.locale.Text("ux_h_" + action), Value: action}
}

func uxHCode(err error) string {
    if err == nil {
        return ""
    }
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return "QUERY_FAILED"
}

func (c *invocation) uxHError(err error, stateError string) string {
    if stateError != "" {
        return c.output.locale.Text("queue_state_save_failed", map[string]any{"Code": stateError})
    }
    return c.output.locale.Text("ux_h_query_error", map[string]any{"Code": uxHCode(err)})
}

func (c *invocation) uxHSummary(target string, result queue.Result, cause error, limit int) string {
    l := c.output.locale
    if target == "" {
        target = l.Text("ux_core_no_machine")
    }
    lines := []string{l.Text("ux_h_target", map[string]any{"Target": uxHText(target)})}
    switch {
    case uxHCode(cause) == "QUEUE_NOT_ENABLED":
        lines = append(lines, l.Text("ux_h_disabled"))
    case result.Status != nil:
        state := "queue_active"
        if result.Status.Paused {
            state = "queue_paused"
        }
        lines = append(lines, l.Text("ux_h_capacity_status", map[string]any{"Value": l.Text(state), "Count": result.Status.Reserved, "Total": result.Status.Slots, "Queued": result.Status.Queued}))
        if !result.Status.CoordinatorRunning {
            lines = append(lines, l.Text("ux_h_inactive"))
        }
    }
    if result.StateError != "" || cause != nil && uxHCode(cause) != "QUEUE_NOT_ENABLED" {
        lines = append(lines, c.uxHError(cause, result.StateError))
    } else if cause == nil && len(result.Requests) == 0 && result.Status != nil {
        lines = append(lines, l.Text("queue_empty"))
    }
    if cause == nil || len(result.Requests) > 0 {
        coverage := l.Text("ux_h_coverage", map[string]any{"Count": len(result.Requests), "Limit": limit})
        if result.HasMore {
            coverage += " · " + l.Text("ux_h_more")
        }
        lines = append(lines, coverage)
    }
    return strings.Join(lines, "\n")
}

func (c *invocation) uxHState(state string) string {
    switch state {
    case "preparing", "queued", "dispatching", "submitted", "running", "completed", "canceled", "unconfirmed", "dispatch_unconfirmed":
        return c.output.locale.Text("queue_state_" + state)
    default:
        return c.output.locale.Text("ux_h_unknown", map[string]any{"Value": uxHText(state)})
    }
}

func (c *invocation) uxHLine(row queue.Request) string {
    width := max(24, min(80, interaction.Width(c.options.Err))-12)
    text := uxHText(row.ID) + " · " + c.uxHState(row.State)
    if row.Name != "" {
        text += " · " + uxHText(row.Name)
    }
    return ansi.Truncate(text, width, "…")
}

func (c *invocation) uxHReason(row queue.Request, status *queue.Status) string {
    l := c.output.locale
    if row.ReasonCode != "" {
        return l.Text("ux_h_reason_code", map[string]any{"Code": uxHText(row.ReasonCode)})
    }
    switch row.State {
    case "preparing":
        return l.Text("ux_h_preparing")
    case "queued":
        if status == nil {
            return l.Text("ux_h_wait_unknown")
        }
        reasons := []string{}
        if status.Paused {
            reasons = append(reasons, l.Text("ux_h_wait_paused"))
        }
        if status.Reserved >= status.Slots {
            reasons = append(reasons, l.Text("ux_h_wait_capacity"))
        }
        if !status.CoordinatorRunning {
            reasons = append(reasons, l.Text("ux_h_inactive"))
        }
        if len(reasons) == 0 {
            reasons = append(reasons, l.Text("ux_h_wait_fifo"))
        }
        return strings.Join(reasons, " · ")
    case "dispatching", "unconfirmed", "dispatch_unconfirmed":
        return l.Text("ux_h_dispatch_uncertain")
    case "submitted", "running", "completed":
        return l.Text("ux_h_dispatched")
    case "canceled":
        return l.Text("ux_h_canceled")
    default:
        return l.Text("ux_h_wait_unknown")
    }
}

func (c *invocation) uxHRequestDocument(result queue.Result, cause error) interaction.Document {
    row := result.Request
    l := c.output.locale
    doc := interaction.Document{Title: l.Text("ux_h_request")}
    if row != nil {
        pairs := []struct{ key, value string }{
            {"request_id", row.ID}, {"state", c.uxHState(row.State)}, {"run_id", row.RunID},
            {"project_id", row.ProjectID}, {"target_id", row.TargetID}, {"name", row.Name},
            {"tags", strings.Join(row.Tags, ", ")}, {"sequence", strconv.FormatUint(row.Sequence, 10)},
            {"created", row.CreatedAt.Format(time.RFC3339)}, {"updated", row.UpdatedAt.Format(time.RFC3339)},
        }
        if row.AttemptedAt != nil {
            pairs = append(pairs, struct{ key, value string }{"attempted", row.AttemptedAt.Format(time.RFC3339)})
        }
        for _, pair := range pairs {
            if pair.value != "" {
                doc.Rows = append(doc.Rows, interaction.Row{Label: l.Text("ux_h_" + pair.key), Value: uxHText(pair.value)})
            }
        }
        doc.Body = c.uxHReason(*row, result.Status) + "\n" + l.Text("ux_h_fixed_run") + "\n" + l.Text("ux_h_command_unavailable")
    }
    if cause != nil {
        doc.Body += "\n" + c.uxHError(cause, result.StateError)
    } else if result.StateError != "" {
        doc.Body += "\n" + l.Text("queue_state_save_failed", map[string]any{"Code": result.StateError})
    }
    return doc
}

func uxHCancelable(state string) bool { return state == "preparing" || state == "queued" }

func (c *invocation) uxHRunRoutes(row queue.Request, local bool) []uxRoute {
    routes := []uxRoute{}
    if !local || row.RunID == "" {
        return routes
    }
    for _, kind := range []string{"task", "logs", "outputs"} {
        if c.uxRouteAvailable(kind) {
            routes = append(routes, uxRoute{Kind: kind, ID: row.RunID})
        }
    }
    return routes
}

func uxHFilter(rows []queue.Request, search string) []queue.Request {
    matches := []queue.Request{}
    search = strings.ToLower(strings.TrimSpace(search))
    for _, row := range rows {
        text := strings.Join([]string{row.ID, row.RunID, row.Name, row.State, row.ProjectID, strings.Join(row.Tags, " ")}, " ")
        if strings.Contains(strings.ToLower(text), search) {
            matches = append(matches, row)
        }
    }
    return matches
}

// Only presentation escapes controls. Opaque IDs and argv retain their bytes.
func uxHText(text string) string {
    return strings.Map(func(r rune) rune {
        if unicode.IsControl(r) {
            return ' '
        }
        return r
    }, text)
}

func uxHQuoted(args []string) string { return fmt.Sprintf("%q", args) }

// uxHPrintQueuePreview is shared by the queue review and direct async presenter.
// The application owns normalization; only its public preview is rendered here.
func uxHPrintQueuePreview(out io.Writer, preview execution.RunPreview, l *i18n.Localizer) error {
    var body strings.Builder
    printPreview(&body, preview, l)
    for _, item := range []struct{ key, value string }{
        {"name", uxHQuoted([]string{preview.Metadata.Name})},
        {"tags", uxHQuoted(preview.Metadata.Tags)},
        {"notifications", uxHQuoted(preview.Notifications)},
    } {
        fmt.Fprintln(&body, l.Text("ux_h_"+item.key)+": "+item.value)
    }
    _, err := io.WriteString(out, body.String())
    return err
}
