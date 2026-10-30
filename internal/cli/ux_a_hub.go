package cli

import (
    "context"
    "errors"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
)

func (c *invocation) uxAMachineHub(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    browser := uxATargetBrowser{}
    for {
        data, err := c.app.Machines.Targets(ctx, "")
        if err != nil {
            return nil, err
        }
        selected, err := browser.choose(ctx, c, data, false)
        if uxACanceled(err) || selected == ":back" {
            return nil, nil
        }
        if err != nil {
            return nil, err
        }
        browser.notice = ""
        switch selected {
        case ":refresh":
            err = c.uxARefreshSettings()
        case ":add":
            var child *invocation
            child, err = c.uxChild("machine", "list")
            if err == nil {
                var result any
                result, err = child.uxAMachineAdd(ctx)
                if added, ok := result.(client.AddResult); ok && err == nil {
                    browser.initial, browser.query, browser.page = added.Target, "", 0
                    browser.positioned = false
                    browser.notice = c.output.locale.Text("ux_a_saved", map[string]any{"Target": c.uxAClip(added.Target, 38)})
                }
            }
        default:
            err = c.uxAMachineDetail(ctx, selected)
        }
        if err != nil && !uxACanceled(err) {
            if showErr := c.uxShowResult(ctx, nil, err); showErr != nil {
                return nil, showErr
            }
        }
    }
}

func (c *invocation) uxAMachineDetail(ctx context.Context, name string) error {
    l := c.output.locale
    for {
        data, err := c.app.Machines.Targets(ctx, name)
        if err != nil {
            return err
        }
        entry := data.Targets[0]
        actions := []string{"details"}
        if entry.Cloud != nil && c.uxRouteAvailable("cloud-info") {
            actions = append(actions, "cloud_info")
        }
        if !entry.Selected {
            actions = append(actions, "use")
        }
        for _, route := range []string{"check", "ssh", "exec", "forward"} {
            if c.uxRouteAvailable(route) {
                actions = append(actions, "connections")
                break
            }
        }
        actions = append(actions, "replace", "remove", "back")
        summary := c.uxAClip(entry.Name, interaction.Width(c.options.Err)-12) + "\n" + c.uxAChoiceSummary(data.Project)
        action, err := c.uxAMenu(ctx, "ux_a_details", summary, "back", actions...)
        if uxACanceled(err) || action == "back" {
            return nil
        }
        if err != nil {
            return err
        }
        var result any
        switch action {
        case "details":
            err = c.uxShowDocument(ctx, c.uxATargetDocument(entry))
        case "cloud_info":
            binding := *entry.Cloud
            result, err = c.uxNavigate(ctx, uxRoute{Kind: "cloud-info", Binding: &binding})
        case "use":
            result, err = c.app.Machines.UseTarget(ctx, entry.Name)
        case "connections":
            err = c.uxAConnections(ctx, entry.Name)
        case "replace":
            var child *invocation
            child, err = c.uxChild("machine", "list")
            if err == nil {
                child.grammar.Machine.Add.Name = entry.Name
                child.grammar.Machine.Add.RemoteDir = entry.RemoteBase
                result, err = child.uxAMachineAdd(ctx)
            }
        case "remove":
            var removed client.RemoveResult
            removed, err = c.uxARemove(ctx, entry.Name)
            if err == nil {
                if !removed.Selected {
                    return c.uxShowResult(ctx, removed, nil)
                }
                // Keep the missing preference until the user explicitly sets
                // another project machine; removal never chooses a fallback.
                next, e := c.uxAMenu(ctx, "ux_a_removed", l.Text("ux_a_removed_selected", map[string]any{"Target": c.uxAClip(entry.Name, 38)}), "back", "choose_project", "back")
                if uxACanceled(e) || next == "back" {
                    return nil
                }
                if e != nil {
                    return e
                }
                picked, e := c.uxAPickTarget(ctx, "")
                if uxACanceled(e) {
                    return nil
                }
                if e != nil {
                    return e
                }
                result, err = c.app.Machines.UseTarget(ctx, picked)
                return c.uxShowResult(ctx, result, err)
            }
        }
        if uxACanceled(err) {
            continue
        }
        if result != nil || err != nil {
            if err := c.uxShowResult(ctx, result, err); err != nil {
                return err
            }
        }
    }
}

func (c *invocation) uxAConnections(ctx context.Context, name string) error {
    actions := []string{}
    for _, route := range []string{"check", "ssh", "exec", "forward"} {
        if c.uxRouteAvailable(route) {
            actions = append(actions, route)
        }
    }
    actions = append(actions, "back")
    action, err := c.uxAMenu(ctx, "ux_a_connections", c.uxAClip(name, interaction.Width(c.options.Err)-12), "back", actions...)
    if err != nil || action == "back" {
        return err
    }
    result, err := c.uxNavigate(ctx, uxRoute{Kind: action, Target: &name})
    if uxACanceled(err) {
        return nil
    }
    if result != nil || err != nil {
        return c.uxShowResult(ctx, result, err)
    }
    return nil
}

func (c *invocation) uxARemove(ctx context.Context, name string) (client.RemoveResult, error) {
    data, err := c.app.Machines.Targets(ctx, name)
    if err != nil {
        return client.RemoveResult{}, err
    }
    doc := c.uxATargetDocument(data.Targets[0])
    doc.Title, doc.Footer = c.output.locale.Text("ux_a_remove"), c.output.locale.Text("ux_a_remove_scope")
    action, err := c.uxAReviewPages(ctx, doc, "remove", false)
    if err != nil {
        return client.RemoveResult{}, err
    }
    if action != "remove" {
        return client.RemoveResult{}, interaction.ErrCanceled
    }
    result, err := c.app.Machines.RemoveTarget(ctx, name)
    if err == nil {
        err = c.uxARefreshSettings()
    }
    return result, err
}

func (c *invocation) uxAMenu(ctx context.Context, title, summary, initial string, actions ...string) (string, error) {
    choices := make([]interaction.Choice, 0, len(actions))
    for _, action := range actions {
        id := "ux_a_" + action
        if action == "back" {
            id = "ux_core_back"
        } else if action == "cloud_info" {
            id = "ux_core_cloud_instance_info"
        }
        choices = append(choices, interaction.Choice{Label: c.output.locale.Text(id), Value: action})
    }
    action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text(title), summary, choices, initial)
    if errors.Is(err, interaction.ErrCanceled) {
        return "", interaction.ErrCanceled
    }
    return action, err
}
