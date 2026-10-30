package cli

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strconv"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

// uxBConnection runs one native operation. The caller may pass A's temporary
// selection; a missing target uses A's selector without writing a preference.
func (c *invocation) uxBConnection(ctx context.Context, kind string, target *string) (any, error) {
    switch kind {
    case "ssh", "exec", "forward":
    default:
        return nil, fault.Invalid("connection", "invalid_value", "ux_b_connection_kind")
    }
    if !c.uxInteractive() || c.output.json {
        return nil, interaction.ErrNonInteractive
    }
    name, err := c.uxBTarget(ctx, target)
    if err != nil {
        return nil, err
    }
    var args []string
    var remote int
    var local *int
    switch kind {
    case "exec":
        args, err = c.uxBEditExec(ctx, name)
    case "forward":
        remote, local, err = c.uxBForwardPorts(ctx, name)
    }
    if err != nil {
        return nil, err
    }
    err = c.input.WithNative(ctx, nil, func() error {
        streams := client.Streams{In: c.options.In, Out: c.output.out, Err: c.output.err}
        switch kind {
        case "ssh":
            return c.app.Machines.SSH(ctx, name, streams)
        case "exec":
            _, err := c.app.Machines.Exec(ctx, client.ExecInput{Target: name, CWD: c.grammar.Exec.CWD, Args: args, Policy: c.policy}, streams)
            return err
        default:
            return c.app.Machines.Forward(ctx, name, remote, local, c.policy, c.output.err, func(event client.Progress) {
                // Sparse progress never owns stdin. Opening is not a health check.
                fmt.Fprintln(c.output.err, c.output.locale.Text("forward_opening", map[string]any{"Target": name, "Value": uxBMapping(event.Count, remote)}))
            })
        }
    })
    if ctx.Err() != nil {
        if err != nil {
            return nil, err
        }
        return nil, ctx.Err()
    }
    if err != nil {
        if _, writeErr := fmt.Fprintln(c.output.err, uxBRecovery(c.output.error(err), c.output.locale)); writeErr != nil {
            return nil, errors.Join(err, writeErr)
        }
    }
    return nil, err
}

func (c *invocation) uxBTarget(ctx context.Context, target *string) (string, error) {
    if target == nil {
        target = c.grammar.Target
    }
    result, err := c.app.Machines.Targets(ctx, deref(target))
    if err != nil {
        return "", err
    }
    if target != nil && *target != "" {
        return *target, nil
    }
    return c.uxAPickTarget(ctx, result.Project.Target)
}

// uxBEditExec shares C's literal argv parser and explicit Shell normalization.
// Exec has no project-command choice: a foreground operation needs explicit argv.
func (c *invocation) uxBEditExec(ctx context.Context, target string) ([]string, error) {
    l := c.output.locale
    mode, err := c.input.SelectWithSummary(ctx, l.Text("ux_b_exec"), target, []interaction.Choice{
        {Label: l.Text("ux_b_argv"), Value: "argv"},
        {Label: l.Text("ux_b_shell"), Value: "shell"},
        {Label: l.Text("ux_core_back"), Value: "back"},
    }, "argv")
    if err != nil {
        return nil, err
    }
    if mode == "back" {
        return nil, interaction.ErrCanceled
    }
    field := &interaction.Field{Title: l.Text("ux_b_" + mode), Description: l.Text("ux_b_" + mode + "_help")}
    field.Validate = func(value string) error {
        if _, err := uxBExecArgs(mode, value); err != nil {
            return errors.New(l.Text("ux_b_command_invalid"))
        }
        return nil
    }
    for {
        if err := c.input.PageWithSummary(ctx, l.Text("ux_b_exec"), target, field); err != nil {
            return nil, err
        }
        args, err := uxBExecArgs(mode, field.Value)
        if err != nil {
            return nil, err
        }
        // Review contains exactly the data which will be sent, never an expanded
        // shell preview. Editing retains the draft; backing out executes nothing.
        encoded, _ := json.Marshal(args)
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_b_exec_review"), target+"\n"+string(encoded), []interaction.Choice{
            {Label: l.Text("ux_b_execute"), Value: "execute"},
            {Label: l.Text("ux_b_edit"), Value: "edit"},
            {Label: l.Text("ux_core_back"), Value: "back"},
        }, "back")
        if err != nil {
            return nil, err
        }
        switch action {
        case "execute":
            return args, nil
        case "back":
            return nil, interaction.ErrCanceled
        }
    }
}

func uxBExecArgs(mode, text string) ([]string, error) {
    switch mode {
    case "argv":
        return uxCParseArgv(text)
    case "shell":
        return uxCShellArgs(text)
    default:
        return nil, fault.Invalid("command", "invalid_value", "ux_b_command_invalid")
    }
}

func (c *invocation) uxBForwardPorts(ctx context.Context, target string) (int, *int, error) {
    l := c.output.locale
    remote := &interaction.Field{Title: l.Text("ux_b_remote_port")}
    local := &interaction.Field{Title: l.Text("ux_b_local_port"), Description: l.Text("ux_b_local_port_help")}
    remote.Validate = func(value string) error {
        if _, err := uxBPort(value, false); err != nil {
            return errors.New(l.Text("forward_port"))
        }
        return nil
    }
    local.Validate = func(value string) error {
        if _, err := uxBPort(value, true); err != nil {
            return errors.New(l.Text("forward_port"))
        }
        return nil
    }
    for {
        if err := c.input.PageWithSummary(ctx, l.Text("ux_b_forward"), target, remote, local); err != nil {
            return 0, nil, err
        }
        remotePort, err := uxBPort(remote.Value, false)
        if err != nil {
            return 0, nil, err
        }
        localPort, err := uxBPort(local.Value, true)
        if err != nil {
            return 0, nil, err
        }
        effective := remotePort
        var override *int
        if local.Value != "" {
            effective, override = localPort, &localPort
        }
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_b_forward_review"), target+"\n"+uxBMapping(effective, remotePort)+"\n"+l.Text("ux_b_forward_scope"), []interaction.Choice{
            {Label: l.Text("ux_b_open"), Value: "open"},
            {Label: l.Text("ux_b_edit"), Value: "edit"},
            {Label: l.Text("ux_core_back"), Value: "back"},
        }, "back")
        if err != nil {
            return 0, nil, err
        }
        switch action {
        case "open":
            return remotePort, override, nil
        case "back":
            return 0, nil, interaction.ErrCanceled
        }
    }
}

func uxBPort(value string, optional bool) (int, error) {
    if value == "" && optional {
        return 0, nil
    }
    port, err := strconv.Atoi(value)
    if err != nil || port < 1 || port > 65535 {
        return 0, fault.Invalid("port/--local-port", "out_of_range", "forward_port")
    }
    return port, nil
}

func uxBMapping(local, remote int) string {
    return fmt.Sprintf("127.0.0.1:%d → 127.0.0.1:%d", local, remote)
}
