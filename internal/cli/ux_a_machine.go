package cli

import (
    "context"
    "errors"
    "fmt"
    "io"
    "strings"
    "unicode"

    "github.com/charmbracelet/x/ansi"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

// uxAMachineName permits human selection while uxAMachineCommand still requires
// an explicit alias in batch/JSON mode.
type uxAMachineName struct {
    Name string `arg:"" optional:"" help:"name_arg" value-type:"name"`
}

// uxAMachineCommand preserves direct calls and owns only the interactive gaps.
// The shared dispatcher delegates the machine command family here.
func (c *invocation) uxAMachineCommand(ctx context.Context, path string) (any, error) {
    g := c.grammar.Machine
    switch path {
    case "machine":
        if c.uxInteractive() {
            return c.uxAMachineHub(ctx)
        }
        return c.app.Machines.Targets(ctx, "")
    case "machine list":
        return c.app.Machines.Targets(ctx, "")
    case "machine add":
        if g.Add.Name == "" || g.Add.SSH == "" {
            if c.uxInteractive() {
                return c.uxAMachineAdd(ctx)
            }
            // These requirements are known locally. Collect all omissions
            // without resolving a connection or exposing supplied values.
            parsed, err := uxJReadCommand([]string{"loaf", "machine", "add"})
            if err != nil {
                return nil, err
            }
            missing := []inputIssue{}
            if g.Add.Name == "" {
                missing = append(missing, inputIssue{Argument: "name", Reason: "missing_argument"})
            }
            if g.Add.SSH == "" {
                missing = append(missing, inputIssue{Argument: "--ssh", Reason: "missing_argument"})
            }
            c.output.issues, err = uxJInputIssues(parsed.node, missing...)
            if err != nil {
                return nil, err
            }
            if g.Add.Name == "" {
                return nil, fault.Invalid("name", "missing_argument", "ux_a_name_required")
            }
            return nil, fault.Invalid("--ssh", "missing_argument", "ssh_required")
        }
        result, err := c.app.Machines.AddTarget(ctx, g.Add.Name, g.Add.SSH, g.Add.RemoteDir, g.Add.Replace)
        if err != nil {
            return nil, err
        }
        return result, c.uxARefreshSettings()
    case "machine use", "machine show", "machine remove":
        name := g.Use.Name
        if path == "machine show" {
            name = g.Show.Name
        } else if path == "machine remove" {
            name = g.Remove.Name
        }
        missing := name == ""
        if missing {
            if !c.uxInteractive() {
                return nil, fault.Invalid("name", "missing_argument", "ux_a_name_required")
            }
            var err error
            name, err = c.uxAPickTarget(ctx, "")
            if err != nil {
                return nil, err
            }
        }
        switch path {
        case "machine use":
            return complete(c.app.Machines.UseTarget(ctx, name))
        case "machine show":
            result, err := c.app.Machines.Targets(ctx, name)
            if err != nil {
                return nil, err
            }
            return machineDetails{result}, err
        default:
            if missing {
                return c.uxARemove(ctx, name)
            }
            result, err := c.app.Machines.RemoveTarget(ctx, name)
            if err != nil {
                return nil, err
            }
            return result, c.uxARefreshSettings()
        }
    }
    return nil, fault.New("BAD_CONFIG", "ux_core_invalid_route", nil)
}

// uxAPickTarget reads registered machines, returning the real alias only after
// an explicit choice. Opening it never creates a project preference.
func (c *invocation) uxAPickTarget(ctx context.Context, initial string) (string, error) {
    if !c.uxInteractive() {
        return "", interaction.ErrNonInteractive
    }
    browser := uxATargetBrowser{initial: initial}
    for {
        data, err := c.app.Machines.Targets(ctx, "")
        if err != nil {
            return "", err
        }
        selected, err := browser.choose(ctx, c, data, true)
        if uxACanceled(err) {
            return "", interaction.ErrCanceled
        }
        if err != nil {
            return "", err
        }
        switch selected {
        case ":back":
            return "", interaction.ErrCanceled
        case ":manage":
            if _, err := c.uxAMachineHub(ctx); err != nil {
                return "", err
            }
        default:
            return selected, nil
        }
    }
}

type uxATargetBrowser struct {
    query, initial, notice string
    page                   int
    positioned             bool
}

func (b *uxATargetBrowser) choose(ctx context.Context, c *invocation, data client.TargetsResult, picking bool) (string, error) {
    l := c.output.locale
    for {
        filtered := uxAFilterTargets(data.Targets, b.query)
        size := max(1, min(6, interaction.Height(c.options.Err)-15))
        if !b.positioned {
            for i, entry := range filtered {
                if entry.Name == b.initial {
                    b.page = i / size
                    break
                }
            }
            b.positioned = true
        }
        pages := max(1, (len(filtered)+size-1)/size)
        b.page = min(b.page, pages-1)
        title, endAction := "ux_a_hub", "ux_core_back"
        if picking {
            title, endAction = "ux_a_pick", "ux_a_cancel"
        }
        summary := c.uxAChoiceSummary(data.Project)
        if b.notice != "" {
            summary += "\n" + b.notice
        }
        if b.query != "" {
            summary += "\n" + l.Text("ux_a_filter", map[string]any{"Value": c.uxAClip(b.query, 30)})
        }
        if len(filtered) == 0 {
            id := "ux_a_no_matches"
            if len(data.Targets) == 0 {
                id = "ux_a_empty"
            }
            summary += "\n" + l.Text(id)
        }
        if pages > 1 {
            summary += "\n" + l.Text("ux_core_page_count", map[string]any{"Count": b.page + 1, "Total": pages})
        }
        choices := []interaction.Choice{}
        for _, entry := range filtered[b.page*size : min(len(filtered), (b.page+1)*size)] {
            marker := "·"
            if entry.Selected {
                marker = "*"
            }
            label := marker + " " + c.uxAKind(entry) + " · " + uxADisplay(entry.Name)
            choices = append(choices, interaction.Choice{Label: label, Value: entry.Name})
        }
        if b.page+1 < pages {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_core_next"), Value: ":next"})
        }
        if b.page > 0 {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_core_previous"), Value: ":previous"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_a_search"), Value: ":search"})
        if picking {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_a_manage"), Value: ":manage"})
        } else {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_a_refresh"), Value: ":refresh"}, interaction.Choice{Label: l.Text("ux_a_add"), Value: ":add"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text(endAction), Value: ":back"})
        current := ":back"
        for _, choice := range choices {
            if choice.Value == b.initial {
                current = b.initial
            }
        }
        selected, err := c.input.SelectWithSummary(ctx, l.Text(title), summary, choices, current)
        if err != nil {
            return "", err
        }
        switch selected {
        case ":next":
            b.page++
        case ":previous":
            b.page--
        case ":search":
            query, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_a_search_prompt"), Description: l.Text("ux_a_search_help"), MaxBytes: 512})
            if errors.Is(err, interaction.ErrCanceled) {
                continue
            }
            if err != nil {
                return "", err
            }
            b.query, b.page = strings.TrimSpace(query), 0
        default:
            b.initial = selected
            return selected, nil
        }
    }
}

func uxAFilterTargets(entries []client.TargetEntry, query string) []client.TargetEntry {
    query = strings.ToLower(strings.TrimSpace(query))
    result := []client.TargetEntry{}
    for _, entry := range entries {
        searchable := entry.Name + " " + entry.Destination
        if entry.Cloud != nil {
            searchable += " " + entry.Cloud.Ref.Account + " " + string(entry.Cloud.Ref.Product) + " " + entry.Cloud.Ref.ID
        }
        if strings.Contains(strings.ToLower(searchable), query) {
            result = append(result, entry)
        }
    }
    return result
}

func (c *invocation) uxAChoiceSummary(choice client.TargetChoice) string {
    id := "ux_a_project_none"
    if choice.Target != "" {
        id = "ux_a_project_current"
        if !choice.Available {
            id = "ux_a_project_missing"
        }
    }
    return c.output.locale.Text(id, map[string]any{"Target": uxADisplay(choice.Target)})
}

func (c *invocation) uxAKind(entry client.TargetEntry) string {
    if entry.Cloud != nil {
        return c.output.locale.Text("ux_a_cloud")
    }
    return c.output.locale.Text("ux_a_static")
}

func (c *invocation) uxATargetDocument(entry client.TargetEntry) interaction.Document {
    l := c.output.locale
    rows := []interaction.Row{{Label: l.Text("ux_a_name"), Value: uxADisplay(entry.Name)}, {Label: l.Text("ux_a_kind"), Value: c.uxAKind(entry)}}
    if entry.Selected {
        rows = append(rows, interaction.Row{Label: l.Text("ux_a_scope"), Value: l.Text("ux_a_selected")})
    }
    if entry.Cloud == nil {
        rows = append(rows, interaction.Row{Label: l.Text("ux_a_destination"), Value: uxADisplay(entry.Destination)})
        if len(entry.Args) > 0 {
            rows = append(rows, interaction.Row{Label: l.Text("targets_ssh_args"), Value: uxADisplay(fmt.Sprintf("%q", entry.Args))})
        }
    } else {
        rows = append(rows,
            interaction.Row{Label: l.Text("ux_a_account"), Value: uxADisplay(entry.Cloud.Ref.Account)},
            interaction.Row{Label: l.Text("ux_a_product"), Value: string(entry.Cloud.Ref.Product)},
            interaction.Row{Label: l.Text("ux_a_instance"), Value: uxADisplay(entry.Cloud.Ref.ID)},
        )
    }
    rows = append(rows, interaction.Row{Label: l.Text("targets_remote_base"), Value: uxADisplay(entry.RemoteBase)})
    return interaction.Document{Title: l.Text("ux_a_details"), Rows: rows}
}

func (c *invocation) uxARefreshSettings() error {
    settings, err := c.app.Machines.Config.Load()
    if err != nil {
        return err
    }
    c.app.Machines.Settings = settings
    if c.app.Projects != nil {
        c.app.Projects.Settings = settings
    }
    if c.app.Resources != nil {
        c.app.Resources.Settings = settings
    }
    return nil
}

func (c *invocation) uxAClip(text string, width int) string {
    return ansi.Truncate(uxADisplay(text), max(8, width), "…")
}

func uxADisplay(text string) string {
    return strings.Map(func(r rune) rune {
        if unicode.IsControl(r) {
            return '�'
        }
        return r
    }, ansi.Strip(text))
}

func uxACanceled(err error) bool {
    return errors.Is(err, interaction.ErrCanceled) || errors.Is(err, io.EOF)
}
