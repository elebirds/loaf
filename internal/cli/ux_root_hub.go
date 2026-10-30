package cli

import (
    "context"
    "errors"
    "path/filepath"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *invocation) uxRoot(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    for {
        summary, err := c.uxRootSummary(ctx)
        if err != nil {
            return nil, err
        }
        action, err := c.input.SelectWithSummary(ctx, "Loaf", summary, c.uxRootChoices(), "")
        if errors.Is(err, interaction.ErrCanceled) {
            return nil, nil
        }
        if err != nil || action == "exit" {
            return nil, err
        }
        if action == "more" {
            action, err = c.uxMore(ctx)
            if errors.Is(err, interaction.ErrCanceled) && ctx.Err() == nil {
                continue
            }
            if err != nil {
                return nil, err
            }
            if action == "back" {
                continue
            }
        }
        data, err := c.uxNavigate(ctx, uxRoute{Kind: action})
        if ctx.Err() != nil {
            return nil, ctx.Err()
        }
        if errors.Is(err, context.Canceled) {
            return nil, err
        }
        if !errors.Is(err, interaction.ErrCanceled) && (data != nil || err != nil) {
            if err := c.uxShowResult(ctx, data, err); err != nil && !errors.Is(err, interaction.ErrCanceled) {
                return nil, err
            }
        }
    }
}

func (c *invocation) uxRootChoices() []interaction.Choice {
    choices := []interaction.Choice{}
    for _, entry := range []struct{ kind, label string }{
        {"run", "ux_core_run"}, {"tasks", "ux_core_tasks"},
        {"results", "ux_core_results"}, {"machine", "ux_core_machines"},
        {"cloud", "ux_core_cloud"},
    } {
        if c.uxRouteAvailable(entry.kind) {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text(entry.label), Value: entry.kind})
        }
    }
    if c.uxRouteAvailable("sync") || c.uxRouteAvailable("queue") || c.uxRouteAvailable("notify") || c.uxRouteAvailable("events") {
        choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_more"), Value: "more"})
    }
    return append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_exit"), Value: "exit"})
}

func (c *invocation) uxRootSummary(ctx context.Context) (string, error) {
    if c.app == nil || c.app.Machines == nil || c.app.Machines.Config == nil {
        return "", fault.New("FEATURE_UNAVAILABLE", "ux_core_unavailable", nil)
    }
    settings, err := c.app.Machines.Config.Load()
    if err != nil {
        return "", err
    }
    c.app.Machines.Settings = settings
    if c.app.Projects != nil {
        c.app.Projects.Settings = settings
    }
    if c.app.Resources != nil {
        c.app.Resources.Settings = settings
    }
    targets, err := c.app.Machines.Targets(ctx, "")
    if err != nil {
        return "", err
    }
    target := targets.Project.Target
    if c.grammar.Target != nil {
        target = *c.grammar.Target
    }
    if target == "" {
        target = c.output.locale.Text("ux_core_no_machine")
    } else if _, ok := settings.Targets[target]; !ok {
        target = c.output.locale.Text("ux_core_missing_machine", map[string]any{"Target": target})
    }
    doc := interaction.Document{Rows: []interaction.Row{
        {Label: c.output.locale.Text("ux_core_project"), Value: filepath.Base(targets.Project.Root)},
        {Label: c.output.locale.Text("ux_core_machine"), Value: target},
    }}
    return doc.Content(max(20, min(96, interaction.Width(c.options.Err))-8), false, c.input.Dark()), nil
}

func (c *invocation) uxMore(ctx context.Context) (string, error) {
    choices := []interaction.Choice{}
    for _, entry := range []struct{ kind, label string }{{"sync", "ux_c_sync_review"}, {"queue", "ux_core_queue"}, {"notify", "ux_core_notify"}, {"events", "ux_i_events"}} {
        if c.uxRouteAvailable(entry.kind) {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text(entry.label), Value: entry.kind})
        }
    }
    choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_back"), Value: "back"})
    return c.input.Select(ctx, c.output.locale.Text("ux_core_more"), choices, "back")
}
