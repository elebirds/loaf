package client

import (
    "context"
    "io"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

// Streams are native operation streams; ownership remains with the caller.
type Streams struct {
    In       io.Reader
    Out, Err io.Writer
}

// ExecInput is a foreground machine operation, not a project submission.
type ExecInput struct {
    Target string
    CWD    *string
    Args   []string
    Policy CallPolicy
}

// ExecResult retains target selection and the transport's confirmed/unconfirmed outcome.
type ExecResult struct {
    Target string
    ssh.ExecResult
}

// Exec preserves argv and never creates a run or synchronizes sources.
func (c *Machines) Exec(ctx context.Context, in ExecInput, streams Streams) (ExecResult, error) {
    if len(in.Args) == 0 {
        return ExecResult{}, fault.New("BAD_CONFIG", "exec_command", nil)
    }
    name, target, file, err := c.machine(ctx, in.Target)
    if err != nil {
        return ExecResult{}, err
    }
    settings := []config.ExecSettings{c.Settings.Exec, file.Exec}
    if in.CWD != nil {
        settings = append(settings, config.ExecSettings{CWD: in.CWD})
    }
    cwd, err := config.ResolveExecCWD(settings...)
    if err != nil {
        return ExecResult{}, err
    }
    if c.Operations == nil {
        return ExecResult{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    observed, err := c.Operations.Exec(ctx, target, ssh.ExecInput{Args: in.Args, CWD: cwd, Batch: in.Policy.Batch}, streams.In, streams.Out, streams.Err)
    return ExecResult{Target: name, ExecResult: observed}, err
}

// SSH yields native terminal ownership to OpenSSH.
func (c *Machines) SSH(ctx context.Context, target string, streams Streams) error {
    _, machine, _, err := c.machine(ctx, target)
    if err != nil {
        return err
    }
    if c.Operations == nil {
        return fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    return c.Operations.Interactive(ctx, machine, streams.In, streams.Out, streams.Err)
}

// Forward opens one foreground tunnel; readiness does not assert application health.
func (c *Machines) Forward(ctx context.Context, target string, remote int, local *int, policy CallPolicy, diagnostics io.Writer, emit ProgressFunc) error {
    port := remote
    if local != nil {
        port = *local
    }
    if remote < 1 || remote > 65535 || port < 1 || port > 65535 {
        return fault.Invalid("port/--local-port", "out_of_range", "forward_port")
    }
    name, machine, _, err := c.machine(ctx, target)
    if err != nil {
        return err
    }
    if c.Operations == nil {
        return fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    progress(emit, Progress{Stage: "forward", Target: name, Count: port})
    return c.Operations.Forward(ctx, machine, port, remote, policy.Batch, diagnostics)
}
