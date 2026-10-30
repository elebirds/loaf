package cli

import (
    "context"
    "errors"
    "fmt"
    "slices"
    "strconv"
    "strings"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
)

// uxFOutputs keeps navigation bound to the chosen run. Direct outputs/pull
// commands do not enter this flow, including their existing latest selection.
func (c *invocation) uxFOutputs(ctx context.Context, id string) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    if id == "" {
        return nil, fault.Invalid("run-id", "missing_argument", "ux_core_run_required")
    }
    return nil, c.uxFBrowse(ctx, id)
}

func (c *invocation) uxFBrowse(ctx context.Context, id string) error {
    l := c.output.locale
    depth, limit := 2, 200
    var selected []string
    var listing execution.OutputListing
    var cause error
    refresh, prepare := true, false
    for {
        if refresh {
            cause = c.input.WithNative(ctx, nil, func() error {
                var err error
                listing, err = c.app.Projects.OutputsList(ctx, id, depth, limit, prepare, c.policy)
                return err
            })
            refresh, prepare = false, false
            if ctx.Err() != nil {
                return ctx.Err()
            }
        }
        summary := c.uxFListingSummary(listing, cause, selected)
        choices := []interaction.Choice{{Label: l.Text("ux_f_entries"), Value: "entries"}}
        usable := cause == nil || uxFCode(cause) == "OUTPUT_MISSING"
        if usable {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_f_choose_paths"), Value: "paths"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_f_review"), Value: "review"}, interaction.Choice{Label: l.Text("ux_f_all_outputs"), Value: "all"})
        if uxFCode(cause) == "RUNNER_REQUIRED" {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_f_prepare"), Value: "prepare"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_f_refresh"), Value: "refresh"}, interaction.Choice{Label: l.Text("ux_f_bounds"), Value: "bounds"}, interaction.Choice{Label: l.Text("ux_f_back"), Value: "back"})
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_f_outputs", map[string]any{"ID": id}), summary, choices, "entries")
        if err != nil {
            return err
        }
        switch action {
        case "entries":
            var body strings.Builder
            printOutputs(&body, listing, l)
            if err := c.uxShowDocument(ctx, interaction.Document{Title: l.Text("ux_f_entries"), Body: body.String()}); err != nil {
                return err
            }
        case "paths":
            available := uxFOutputChoices(listing)
            if len(available) == 0 {
                if err := c.uxShowDocument(ctx, interaction.Document{Body: l.Text("ux_f_no_choices")}); err != nil {
                    return err
                }
                continue
            }
            selected, err = c.uxFSelectPaths(ctx, available, selected, l.Text("ux_f_path_help"))
            if err != nil {
                return err
            }
        case "all":
            selected = nil
        case "review":
            if selected != nil && len(selected) == 0 {
                if err := c.uxShowDocument(ctx, interaction.Document{Body: l.Text("ux_f_selection_empty")}); err != nil {
                    return err
                }
                continue
            }
            if err := c.uxFDownload(ctx, client.PullInput{IDs: []string{id}, Paths: slices.Clone(selected), Policy: c.policy}); err != nil {
                return err
            }
            // Download results can lead to explicit cleanup. Do not reopen a
            // cached inventory for a payload whose local receipt now records
            // deletion or an unconfirmed deletion. This is a local read only.
            if saved, err := c.app.Projects.Inspect(ctx, id); err == nil && (saved.CleanedAt != nil || saved.CleanupPending) {
                return nil
            }
        case "refresh":
            refresh = true
        case "prepare":
            yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: l.Text("ux_f_prepare_title", map[string]any{"ID": id}), Description: l.Text("ux_f_prepare_body"), AcceptLabel: l.Text("yes"), RejectLabel: l.Text("no")})
            if err != nil {
                return err
            }
            if yes {
                refresh, prepare = true, true
            }
        case "bounds":
            d := &interaction.Field{Title: l.Text("ux_f_depth"), Value: strconv.Itoa(depth), Validate: c.uxFInteger(0, 20)}
            n := &interaction.Field{Title: l.Text("ux_f_limit"), Value: strconv.Itoa(limit), Validate: c.uxFInteger(1, 1000)}
            if err := c.input.Page(ctx, l.Text("ux_f_bounds"), d, n); err != nil {
                return err
            }
            depth, _ = strconv.Atoi(d.Value)
            limit, _ = strconv.Atoi(n.Value)
            refresh = true
        default:
            return nil
        }
    }
}

func (c *invocation) uxFInteger(minimum, maximum int) func(string) error {
    return func(value string) error {
        number, err := strconv.Atoi(value)
        if err != nil || number < minimum || number > maximum {
            return errors.New(c.output.locale.Text("ux_f_bound_range", map[string]any{"Min": minimum, "Max": maximum}))
        }
        return nil
    }
}

func uxFCode(err error) string {
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return ""
}

func (c *invocation) uxFListingSummary(r execution.OutputListing, cause error, selected []string) string {
    l := c.output.locale
    lines := []string{}
    if cause != nil && uxFCode(cause) != "OUTPUT_MISSING" {
        e := c.output.error(cause)
        lines = append(lines, e.Code+": "+e.Message)
    } else {
        lines = append(lines, l.Text("ux_f_coverage", map[string]any{"Count": r.Examined, "Examined": r.RootsExamined, "Total": r.RootsTotal, "Depth": r.Depth, "Limit": r.Limit}))
        if r.Truncated || r.DepthLimited > 0 {
            lines = append(lines, l.Text("ux_f_limited", map[string]any{"Count": r.DepthLimited}))
        } else if len(r.Entries) == 0 && len(r.Missing) == 0 {
            lines = append(lines, l.Text("ux_f_empty"))
        }
        if len(r.Missing) > 0 {
            lines = append(lines, l.Text("ux_f_missing", map[string]any{"Count": len(r.Missing)}))
        }
    }
    lines = append(lines, c.uxFSelectionLabel(selected))
    return strings.Join(lines, "\n")
}

func (c *invocation) uxFSelectionLabel(paths []string) string {
    if paths == nil {
        return c.output.locale.Text("ux_f_selection_all")
    }
    return c.output.locale.Text("ux_f_selection_count", map[string]any{"Count": len(paths)})
}

func uxFOutputChoices(r execution.OutputListing) []interaction.Choice {
    choices := []interaction.Choice{}
    seen := map[string]bool{}
    for _, entry := range r.Entries {
        if entry.Type != "file" && entry.Type != "directory" || seen[entry.Path] {
            continue
        }
        if _, err := sources.OutputPaths([]string{entry.Path}); err != nil {
            continue
        }
        inside := false
        for _, root := range r.Paths {
            root = strings.TrimSuffix(root, "/")
            inside = inside || entry.Path == root || strings.HasPrefix(entry.Path, root+"/")
        }
        if !inside {
            continue
        }
        seen[entry.Path] = true
        label := strconv.Quote(entry.Path)
        if entry.Type == "directory" {
            label += "/"
        }
        choices = append(choices, interaction.Choice{Label: label, Value: entry.Path})
    }
    return choices
}

// uxFSelectPaths pages only the already bounded inventory; changing pages never
// performs another remote query. Off-page and temporarily absent choices stay
// selected until explicitly changed, so back/refresh cannot silently widen it.
func (c *invocation) uxFSelectPaths(ctx context.Context, choices []interaction.Choice, initial []string, help string) ([]string, error) {
    selected := append([]string{}, initial...)
    l := c.output.locale
    size := max(1, min(5, interaction.Height(c.options.Err)-16))
    page := 0
    for {
        start, end := page*size, min(len(choices), (page+1)*size)
        shown := slices.Clone(choices[start:end])
        for i := range shown {
            shown[i].Label = ansi.Truncate(shown[i].Label, max(16, interaction.Width(c.options.Err)-16), "…")
        }
        values := []string{}
        for _, choice := range shown {
            if slices.Contains(selected, choice.Value) {
                values = append(values, choice.Value)
            }
        }
        field := &interaction.Field{Title: l.Text("ux_f_choose_paths"), Choices: shown, Values: values, Multiple: true}
        summary := l.Text("ux_core_page_count", map[string]any{"Count": page + 1, "Total": (len(choices) + size - 1) / size})
        if err := c.input.PageWithSummary(ctx, l.Text("ux_f_results"), summary, field); err != nil {
            return selected, err
        }
        for _, choice := range shown {
            selected = slices.DeleteFunc(selected, func(v string) bool { return v == choice.Value })
        }
        selected = append(selected, field.Values...)
        actions := []interaction.Choice{{Label: l.Text("ux_f_use_selection"), Value: "done"}}
        if end < len(choices) {
            actions = append(actions, interaction.Choice{Label: l.Text("ux_core_next"), Value: "next"})
        }
        if page > 0 {
            actions = append(actions, interaction.Choice{Label: l.Text("ux_core_previous"), Value: "previous"})
        }
        actions = append(actions, interaction.Choice{Label: l.Text("ux_core_details"), Value: "details"}, interaction.Choice{Label: l.Text("ux_f_back"), Value: "back"})
        action, err := c.input.SelectWithSummary(ctx, c.uxFSelectionLabel(selected), summary, actions, "done")
        if err != nil {
            return selected, err
        }
        switch action {
        case "next":
            page++
        case "previous":
            page--
        case "details":
            lines := []string{help}
            for _, choice := range choices[start:end] {
                lines = append(lines, strconv.Quote(choice.Value))
            }
            if err := c.uxShowDocument(ctx, interaction.Document{Title: l.Text("ux_f_choose_paths"), Body: strings.Join(lines, "\n")}); err != nil {
                return selected, err
            }
        default:
            return selected, nil
        }
    }
}

func (c *invocation) uxFPickSelectedRun(ctx context.Context, ids []string) (string, error) {
    choices := make([]interaction.Choice, 0, len(ids))
    for _, id := range ids {
        choices = append(choices, interaction.Choice{Label: id, Value: id})
    }
    // Huh keeps a bounded, searchable viewport. Plain terminals page IDs too.
    page, size := 0, max(1, min(6, interaction.Height(c.options.Err)-12))
    for {
        end := min(len(choices), (page+1)*size)
        rows := slices.Clone(choices[page*size : end])
        if end < len(choices) {
            rows = append(rows, interaction.Choice{Label: c.output.locale.Text("ux_core_next"), Value: ":next"})
        }
        if page > 0 {
            rows = append(rows, interaction.Choice{Label: c.output.locale.Text("ux_core_previous"), Value: ":previous"})
        }
        rows = append(rows, interaction.Choice{Label: c.output.locale.Text("ux_f_back"), Value: ":back"})
        id, err := c.input.Select(ctx, c.output.locale.Text("ux_f_browse_run"), rows, ":back")
        if err != nil {
            return "", err
        }
        switch id {
        case ":next":
            page++
        case ":previous":
            page--
        case ":back":
            return "", nil
        default:
            return id, nil
        }
    }
}

func (c *invocation) uxFProgress(event client.Progress) {
    if event.Stage == "pull" {
        fmt.Fprintln(c.output.err, c.output.locale.Text("ux_f_working", map[string]any{"ID": event.RunID}))
    }
}
