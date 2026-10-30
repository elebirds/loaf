package cli

import (
    "context"
    "errors"
    "strings"

    "charm.land/lipgloss/v2"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

type uxAAddDraft struct {
    name, connection, remote string
}

func (c *invocation) uxAMachineAdd(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    input := c.grammar.Machine.Add
    draft := uxAAddDraft{name: input.Name, connection: input.SSH, remote: input.RemoteDir}
    if draft.remote == "" {
        draft.remote = "~/.local/share/loaf/projects"
    }
    if err := c.uxAConnectionPage(ctx, &draft, true); err != nil {
        return nil, err
    }
    for {
        // Refresh before review, not during field validation. Config.Add still
        // arbitrates name conflicts under its existing local settings lock.
        if err := c.uxARefreshSettings(); err != nil {
            return nil, err
        }
        data, err := c.app.Machines.Targets(ctx, "")
        if err != nil {
            return nil, err
        }
        var old *client.TargetEntry
        for i := range data.Targets {
            if data.Targets[i].Name == draft.name {
                old = &data.Targets[i]
                break
            }
        }
        action, err := c.uxAReviewConnection(ctx, draft, old)
        if err != nil {
            return nil, err
        }
        switch action {
        case "cancel":
            return nil, interaction.ErrCanceled
        case "edit":
            if err := c.uxAEditConnection(ctx, &draft); err != nil && !errors.Is(err, interaction.ErrCanceled) {
                return nil, err
            }
        case "remote":
            field := &interaction.Field{Title: c.output.locale.Text("targets_remote_base"), Description: c.output.locale.Text("ux_a_remote_help"), Value: draft.remote, Validate: c.uxAValidateRemote}
            err := c.input.Page(ctx, c.output.locale.Text("ux_a_remote"), field)
            if err == nil {
                draft.remote = field.Value
            } else if !errors.Is(err, interaction.ErrCanceled) {
                return nil, err
            }
        case "save", "replace":
            // Only this explicit review action enables replacement. A partial
            // command's --replace flag does not pre-approve edited form values.
            result, err := c.app.Machines.AddTarget(ctx, draft.name, draft.connection, draft.remote, action == "replace")
            if err != nil {
                if showErr := c.uxShowResult(ctx, nil, err); showErr != nil {
                    return nil, showErr
                }
                continue
            }
            return result, c.uxARefreshSettings()
        }
    }
}

func (c *invocation) uxAEditConnection(ctx context.Context, draft *uxAAddDraft) error {
    return c.uxAConnectionPage(ctx, draft, false)
}

func (c *invocation) uxAConnectionPage(ctx context.Context, draft *uxAAddDraft, missingOnly bool) error {
    l := c.output.locale
    name := &interaction.Field{Title: l.Text("ux_a_name"), Description: l.Text("ux_a_name_help"), Value: draft.name, Validate: func(value string) error {
        if !config.ValidName(value) {
            return errors.New(l.Text("ux_a_name_invalid"))
        }
        return nil
    }}
    connection := &interaction.Field{Title: l.Text("ux_a_connection"), Description: l.Text("ux_a_connection_help"), Value: draft.connection, Validate: func(value string) error {
        _, err := ssh.ParseConnection(value)
        if err != nil {
            return errors.New(l.Text("ssh_input"))
        }
        return nil
    }}
    fields := []*interaction.Field{}
    if !missingOnly || !config.ValidName(draft.name) {
        fields = append(fields, name)
    }
    if _, err := ssh.ParseConnection(draft.connection); !missingOnly || err != nil {
        fields = append(fields, connection)
    }
    if len(fields) == 0 {
        return nil
    }
    if err := c.input.Page(ctx, l.Text("ux_a_add"), fields...); err != nil {
        return err
    }
    draft.name, draft.connection = name.Value, connection.Value
    return nil
}

func (c *invocation) uxAValidateRemote(value string) error {
    if !config.ValidRemotePath(value) {
        return errors.New(c.output.locale.Text("ux_a_remote_invalid"))
    }
    return nil
}

func (c *invocation) uxAReviewConnection(ctx context.Context, draft uxAAddDraft, old *client.TargetEntry) (string, error) {
    l := c.output.locale
    target, err := ssh.ParseConnection(draft.connection)
    if err != nil {
        return "", err
    }
    if !config.ValidName(draft.name) || !config.ValidRemotePath(draft.remote) {
        return "", fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    target.RemoteBase = draft.remote
    next := c.uxATargetDocument(client.TargetEntry{Name: draft.name, Target: target})
    title, save, scope := "ux_a_review", "save", "ux_a_add_scope"
    next.Title = ""
    if old != nil {
        previous := c.uxATargetDocument(*old)
        previous.Title = l.Text("ux_a_old")
        next.Title = l.Text("ux_a_new")
        width := max(20, min(96, interaction.Width(c.options.Err))-8)
        next = interaction.Document{Body: previous.Content(width, false, c.input.Dark()) + "\n\n" + next.Content(width, false, c.input.Dark())}
        title, save, scope = "ux_a_replace_review", "replace", "ux_a_replace_scope"
    }
    next.Title, next.Footer = l.Text(title), l.Text(scope)
    return c.uxAReviewPages(ctx, next, save, true)
}

// uxAReviewPages keeps the scope and exact connection visible before offering
// the local write. Paging never approves a save, replacement or alias removal.
func (c *invocation) uxAReviewPages(ctx context.Context, doc interaction.Document, accept string, editable bool) (string, error) {
    title := doc.Title
    doc.Title = ""
    width := max(20, min(96, interaction.Width(c.options.Err))-8)
    lines := strings.Split(strings.TrimSpace(lipgloss.NewStyle().Width(width).Render(doc.Content(width, false, c.input.Dark()))), "\n")
    size := max(3, interaction.Height(c.options.Err)-13)
    pages := max(1, (len(lines)+size-1)/size)
    for page := 0; ; {
        end := min(len(lines), (page+1)*size)
        summary := strings.Join(lines[page*size:end], "\n")
        if pages > 1 {
            summary += "\n" + c.output.locale.Text("ux_core_page_count", map[string]any{"Count": page + 1, "Total": pages})
        }
        choices := []interaction.Choice{}
        initial := "cancel"
        if end < len(lines) {
            initial = "next"
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_next"), Value: "next"})
        } else {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_a_" + accept), Value: accept})
        }
        if page > 0 {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_previous"), Value: "previous"})
        }
        if editable {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_a_edit"), Value: "edit"}, interaction.Choice{Label: c.output.locale.Text("ux_a_remote"), Value: "remote"})
        }
        choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_a_cancel"), Value: "cancel"})
        action, err := c.input.SelectWithSummary(ctx, title, summary, choices, initial)
        if err != nil {
            return "", err
        }
        switch action {
        case "next":
            page++
        case "previous":
            page--
        default:
            return action, nil
        }
    }
}
