package cli

import (
    "context"
    "errors"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

func (c *invocation) machine(ctx context.Context, path string) (any, error) {
    g := c.grammar.Machine
    switch path {
    case "machine", "machine list":
        return c.app.Machines.Targets(ctx, "")
    case "machine add":
        if g.Add.Name == "" || g.Add.SSH == "" {
            if c.ui {
                if !c.options.Interactive {
                    return nil, fault.Invalid("--interactive", "invalid_value", "ui_terminal_required")
                }
                if err := c.machineAddInputs(ctx, &g.Add); err != nil {
                    return nil, err
                }
            } else {
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
        }
        return complete(c.app.Machines.AddTarget(ctx, g.Add.Name, g.Add.SSH, g.Add.RemoteDir, g.Add.Replace))
    case "machine use", "machine show", "machine remove":
        name := g.Use.Name
        if path == "machine show" {
            name = g.Show.Name
        } else if path == "machine remove" {
            name = g.Remove.Name
        }
        if name == "" {
            return nil, fault.Invalid("name", "missing_argument", "ux_a_name_required")
        }
        switch path {
        case "machine use":
            return complete(c.app.Machines.UseTarget(ctx, name))
        case "machine show":
            data, err := c.app.Machines.Targets(ctx, name)
            if err != nil {
                return nil, err
            }
            return machineDetails{data}, nil
        default:
            return complete(c.app.Machines.RemoveTarget(ctx, name))
        }
    }
    return nil, fault.New("BAD_CONFIG", "invalid_command", nil)
}

// machineAddInputs fills only missing fields for this one local alias write.
// Existing aliases still require the caller's explicit --replace flag.
func (c *invocation) machineAddInputs(ctx context.Context, in *addCommand) error {
    l := c.output.locale
    fields := []*interaction.Field{}
    name := &interaction.Field{Title: l.Text("ux_a_name"), Value: in.Name, Validate: func(value string) error {
        if !config.ValidName(value) {
            return errors.New(l.Text("ux_a_name_invalid"))
        }
        return nil
    }}
    connection := &interaction.Field{Title: l.Text("ux_a_connection"), Value: in.SSH, Validate: func(value string) error {
        if _, err := ssh.ParseConnection(value); err != nil {
            return errors.New(l.Text("ssh_input"))
        }
        return nil
    }}
    if in.Name == "" {
        fields = append(fields, name)
    }
    if in.SSH == "" {
        fields = append(fields, connection)
    }
    if err := c.input.Page(ctx, l.Text("ux_a_add"), fields...); err != nil {
        return err
    }
    in.Name, in.SSH = name.Value, connection.Value
    return nil
}
