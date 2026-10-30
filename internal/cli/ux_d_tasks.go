package cli

import (
    "context"
    "errors"
    "fmt"
    "slices"
    "strings"

    "github.com/alecthomas/kong"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
)

// uxDStatusOptions uses Kong's explicit path rather than comparing values to
// defaults, so --wait 0s and --local=false remain direct requests.
func uxDStatusOptions(parsed *kong.Context) bool {
    node := tracedNode(parsed)
    if node.Path() != "status" {
        return false
    }
    for _, item := range parsed.Path {
        if item.Flag != nil && slices.Contains(node.Flags, item.Flag) {
            return true
        }
    }
    return false
}

// uxDStatusInteractive only changes the bare human entry. Explicit status
// queries, including an explicit zero wait, retain the finite command contract.
func (c *invocation) uxDStatusInteractive(explicitStatusOptions bool) bool {
    v := c.grammar.PS
    return c.options.Interactive && c.uxInteractive() && !c.output.json && !c.grammar.Trace && !explicitStatusOptions &&
        v.ID == "" && !v.Local && (v.Wait == "" || v.Wait == "0s") && v.Limit == nil && c.grammar.Target == nil &&
        v.Name == nil && v.Since == nil && v.Until == nil && v.Status == nil && v.Tag == nil
}

func (c *invocation) uxDTasks(ctx context.Context) (any, error) {
    _, err := c.uxDBrowse(ctx, false, false)
    return nil, err
}

// uxDPickRuns navigates local project facts only, even when used from a live
// task screen. It never selects latest or reaches into a different project.
func (c *invocation) uxDPickRuns(ctx context.Context, multiple bool) ([]string, error) {
    return c.uxDBrowse(ctx, true, multiple)
}

type uxDBrowser struct {
    input    client.PSInput
    history  execution.History
    cause    error
    search   string
    page     int
    focus    string
    selected []string
}

func (c *invocation) uxDLoad(ctx context.Context, b *uxDBrowser) error {
    var result client.PSResult
    var err error
    if b.input.Local {
        result, err = c.app.Projects.PS(ctx, b.input)
    } else {
        err = c.input.WithNative(ctx, nil, func() error {
            result, err = c.app.Projects.PS(ctx, b.input)
            return err
        })
    }
    if ctx.Err() != nil {
        return ctx.Err()
    }
    if result.History == nil {
        return err
    }
    // Partial history is still useful. Validation/read failures must not erase
    // the previous page or masquerade as an empty successful query.
    if err != nil {
        var failure *fault.Error
        if !errors.As(err, &failure) || failure.Code != "QUERY_PARTIAL" && failure.Code != "STATE_PARTIAL" {
            return err
        }
    }
    b.history, b.cause = *result.History, err
    return nil
}

func (b *uxDBrowser) rows() []execution.View {
    rows := []execution.View{}
    query := strings.ToLower(b.search)
    for _, v := range b.history.Runs {
        text := strings.Join([]string{v.ID, v.Name, strings.Join(v.Tags, " "), v.Target, v.Status, v.Phase}, " ")
        if strings.Contains(strings.ToLower(text), query) {
            rows = append(rows, v)
        }
    }
    return rows
}

func (c *invocation) uxDBrowse(ctx context.Context, picking, multiple bool) ([]string, error) {
    if !c.uxInteractive() || c.output.json {
        return nil, interaction.ErrNonInteractive
    }
    b := uxDBrowser{input: client.PSInput{Local: true, Policy: c.policy}}
    if err := c.uxDLoad(ctx, &b); err != nil {
        return nil, err
    }
    l := c.output.locale
    for {
        rows := b.rows()
        size := max(1, min(5, (interaction.Height(c.options.Err)-13)/2))
        b.page = min(b.page, max(0, (len(rows)-1)/size))
        choices := []interaction.Choice{}
        for _, v := range rows[min(b.page*size, len(rows)):min((b.page+1)*size, len(rows))] {
            label := c.uxDRunRow(v)
            if multiple && slices.Contains(b.selected, v.ID) {
                label = "+ " + label
            }
            choices = append(choices, interaction.Choice{Label: label, Value: v.ID})
        }
        if multiple && len(b.selected) > 0 {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_d_use_selection", map[string]any{"Count": len(b.selected)}), Value: "use"})
        }
        if (b.page+1)*size < len(rows) {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_core_next"), Value: "next"})
        }
        if b.page > 0 {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_core_previous"), Value: "previous"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_d_actions"), Value: "actions"}, interaction.Choice{Label: l.Text("ux_core_back"), Value: "back"})
        title := l.Text("ux_d_tasks")
        if picking {
            title = l.Text("ux_d_pick")
        }
        summary := c.uxDBrowserSummary(b, len(rows))
        if len(rows) > 0 {
            summary += "\n    " + c.uxDColumns()
        }
        action, err := c.input.SelectWithSummary(ctx, title, summary, choices, b.focus)
        if errors.Is(err, interaction.ErrCanceled) || action == "back" {
            return nil, nil
        }
        if err != nil {
            return nil, err
        }
        switch action {
        case "next":
            b.page++
            b.focus = ""
        case "previous":
            b.page--
            b.focus = ""
        case "use":
            return slices.Clone(b.selected), nil
        case "actions":
            id, err := c.uxDBrowserActions(ctx, &b, picking, multiple)
            if err != nil {
                if err := c.uxDRecover(ctx, err); err != nil {
                    return nil, err
                }
            }
            if id != "" {
                if picking && !multiple {
                    return []string{id}, nil
                }
                if multiple {
                    b.selected = uxDToggle(b.selected, id)
                } else if _, err := c.uxDTask(ctx, id); err != nil {
                    if err := c.uxDRecover(ctx, err); err != nil {
                        return nil, err
                    }
                }
            }
        default:
            b.focus = action
            if multiple {
                b.selected = uxDToggle(b.selected, action)
                continue
            }
            if picking {
                return []string{action}, nil
            }
            var seed *execution.View
            if !b.input.Local {
                for _, row := range rows {
                    if row.ID == action {
                        seed = &row
                        break
                    }
                }
            }
            if err := c.uxDTaskScreen(ctx, action, seed); err != nil {
                if err := c.uxDRecover(ctx, err); err != nil {
                    return nil, err
                }
            }
        }
    }
}

func uxDToggle(ids []string, id string) []string {
    if i := slices.Index(ids, id); i >= 0 {
        return slices.Delete(ids, i, i+1)
    }
    return append(ids, id)
}

func (c *invocation) uxDBrowserActions(ctx context.Context, b *uxDBrowser, picking, multiple bool) (string, error) {
    l := c.output.locale
    choices := []interaction.Choice{
        {Label: l.Text("ux_d_search"), Value: "search"},
        {Label: l.Text("ux_d_filters"), Value: "filters"},
        {Label: l.Text("ux_d_reload"), Value: "local"},
    }
    if !picking {
        choices = append(choices, interaction.Choice{Label: l.Text("ux_d_refresh"), Value: "live"}, interaction.Choice{Label: l.Text("ux_d_wait_selection"), Value: "wait"})
    }
    choices = append(choices, interaction.Choice{Label: l.Text("ux_d_open_id"), Value: "id"}, interaction.Choice{Label: l.Text("ux_d_coverage"), Value: "coverage"})
    if multiple && len(b.selected) > 0 {
        choices = append(choices, interaction.Choice{Label: l.Text("ux_d_clear_selection"), Value: "clear"})
    }
    choices = append(choices, interaction.Choice{Label: l.Text("ux_core_back"), Value: "back"})
    action, err := c.input.Select(ctx, l.Text("ux_d_actions"), choices, "back")
    if err != nil {
        return "", err
    }
    switch action {
    case "search":
        search, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_d_search"), Description: l.Text("ux_d_search_help", map[string]any{"Value": uxDSafe(b.search)}), MaxBytes: 512})
        if err == nil {
            b.search, b.page, b.focus = search, 0, ""
        }
        return "", err
    case "filters":
        next, apply, err := c.uxDFilters(ctx, b.input)
        if err != nil || !apply {
            return "", err
        }
        copy := *b
        copy.input = next
        if err := c.uxDLoad(ctx, &copy); err != nil {
            return "", err
        }
        copy.page, copy.focus = 0, ""
        *b = copy
    case "live", "local":
        copy := *b
        copy.input.Local = action == "local"
        if copy.input.Local {
            copy.input.Statuses = nil
        }
        if err := c.uxDLoad(ctx, &copy); err != nil {
            return "", err
        }
        *b = copy
    case "wait":
        ids, err := c.uxDPickRuns(ctx, true)
        if err != nil || len(ids) == 0 {
            return "", err
        }
        _, err = c.uxDWait(ctx, ids)
        return "", err
    case "id":
        id, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_d_open_id"), Description: l.Text("ux_d_id_help"), MaxBytes: 128})
        if err != nil || id == "" {
            return "", err
        }
        detail, err := c.app.Projects.Inspect(ctx, id)
        if err != nil {
            return "", err
        }
        return detail.ID, nil
    case "coverage":
        return "", c.uxShowDocument(ctx, c.uxDCoverage(*b))
    case "clear":
        b.selected = nil
    }
    return "", nil
}

func (c *invocation) uxDRecover(ctx context.Context, err error) error {
    if ctx.Err() != nil {
        return ctx.Err()
    }
    if err == nil || errors.Is(err, interaction.ErrCanceled) {
        return nil
    }
    return c.uxShowResult(ctx, nil, err)
}

func (c *invocation) uxDBrowserSummary(b uxDBrowser, shown int) string {
    l := c.output.locale
    mode := "ux_d_saved"
    if !b.input.Local {
        mode = "ux_d_observed"
    }
    summary := l.Text(mode) + " · " + l.Text("ux_d_list_count", map[string]any{"Count": shown, "Total": b.history.Eligible, "Limit": b.history.Limit})
    if shown == 0 {
        summary += "\n" + l.Text("ux_d_empty")
    } else if b.history.Unresolved > 0 || b.history.StateErrors > 0 || b.cause != nil {
        summary += "\n" + l.Text("ux_d_partial", map[string]any{"Query": b.history.Unresolved, "State": b.history.StateErrors, "Unexamined": max(0, b.history.Eligible-b.history.Examined)})
    } else if b.search != "" || len(b.selected) > 0 {
        summary += "\n" + l.Text("ux_d_search_selection", map[string]any{"Value": uxDSafe(b.search), "Count": len(b.selected)})
    }
    return summary
}

func (c *invocation) uxDCoverage(b uxDBrowser) interaction.Document {
    h, l := b.history, c.output.locale
    doc := interaction.Document{Title: l.Text("ux_d_coverage"), Body: l.Text("ux_d_coverage_values", map[string]any{
        "Total": h.Total, "Eligible": h.Eligible, "Examined": h.Examined, "Matched": h.Matched,
        "Unresolved": h.Unresolved, "Unexamined": max(0, h.Eligible-h.Examined), "State": h.StateErrors, "Limit": h.Limit,
    })}
    doc.Body += "\n" + c.uxDFilterSummary(b.input)
    if b.cause != nil {
        e := c.output.error(b.cause)
        doc.Body += fmt.Sprintf("\n%s: %s", e.Code, e.Message)
    }
    return doc
}
