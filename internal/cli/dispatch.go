package cli

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "runtime/debug"
    "strings"
    "sync"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
)

type invocation struct {
    app     *client.App
    options Options
    grammar *grammar
    output  *presenter
    input   *interaction.InputSession
    policy  client.CallPolicy
}

// dispatch only adapts parsed input and presentation callbacks to application calls.
func (c *invocation) dispatch(ctx context.Context, path string) (any, error) {
    g, p := c.grammar, c.output
    switch path {
    case "version":
        info, _ := debug.ReadBuildInfo()
        return versionData(info), nil
    case "machine add":
        connection := g.Machine.Add.SSH
        if connection == "" {
            if c.policy.Batch {
                return nil, fault.Invalid("--ssh", "missing_argument", "ssh_required")
            }
            var err error
            connection, err = c.input.Text(ctx, interaction.TextPrompt{Title: p.locale.Text("ssh_prompt"), Description: p.locale.Text("ssh_prompt_help"), MaxBytes: 8192, Validate: func(value string) error {
                if _, err := ssh.ParseConnection(value); err != nil {
                    var f *fault.Error
                    if errors.As(err, &f) {
                        return errors.New(p.locale.Text(f.Message, f.Params))
                    }
                    return errors.New(p.locale.Text("ssh_input"))
                }
                return nil
            }})
            if err != nil {
                return nil, err
            }
        }
        return complete(c.app.Machines.AddTarget(ctx, g.Machine.Add.Name, connection, g.Machine.Add.RemoteDir, g.Machine.Add.Replace))
    case "machine use":
        return complete(c.app.Machines.UseTarget(ctx, g.Machine.Use.Name))
    case "machine", "machine list", "machine show":
        result, err := c.app.Machines.Targets(ctx, g.Machine.Show.Name)
        if err != nil {
            return nil, err
        }
        if path == "machine show" {
            return machineDetails{result}, nil
        }
        return result, nil
    case "machine remove":
        return complete(c.app.Machines.RemoveTarget(ctx, g.Machine.Remove.Name))
    case "check":
        v := g.Check
        result, err := c.app.Machines.Check(ctx, client.CheckInput{Target: g.Target, Targets: v.Targets.Values, Timeout: v.Timeout, Runner: v.Runner, Report: v.Report, Policy: c.policy})
        if result.Single != nil {
            return p.check(*result.Single), err
        }
        if result.Batch != nil {
            return p.checks(*result.Batch), err
        }
        return nil, err
    case "sync":
        v := g.Sync
        result, err := c.app.Projects.Sync(ctx, client.SyncInput{Target: g.Target, DryRun: v.DryRun, Mode: v.Mode, RespectGitignore: v.RespectGitignore, Exclude: v.Exclude, Policy: c.policy}, c.progress)
        if err != nil {
            return nil, err
        }
        return result, nil
    case "run":
        return c.run(ctx)
    case "status":
        v := g.PS
        result, err := c.app.Projects.PS(ctx, client.PSInput{ID: v.ID, Local: v.Local, Wait: v.Wait, Limit: v.Limit, Target: g.Target, Name: v.Name, Since: v.Since, Until: v.Until, Statuses: v.Status, Tags: v.Tag, Policy: c.policy})
        if result.View != nil {
            return *result.View, err
        }
        if result.Details != nil && result.Details.ID != "" {
            return *result.Details, err
        }
        if result.History != nil {
            return *result.History, err
        }
        return nil, err
    case "inspect":
        result, err := c.app.Projects.Inspect(ctx, g.Inspect.ID)
        if result.ID == "" {
            return nil, err
        }
        return result, err
    case "stop":
        return c.app.Projects.Stop(ctx, client.StopInput{ID: g.Stop.ID, Grace: g.Stop.Grace, Policy: c.policy}, c.progress)
    case "wait":
        var changed func(execution.View)
        if !p.json {
            changed = func(v execution.View) { printRun(p.err, v, p.locale) }
        }
        return c.app.Projects.Wait(ctx, g.Wait.IDs, g.Wait.Tag, g.Wait.Timeout, c.policy, changed)
    case "logs":
        return c.logs(ctx)
    case "outputs":
        v := g.Outputs
        return c.app.Projects.OutputsList(ctx, v.ID, v.Depth, v.Limit, v.PrepareRunner, c.policy)
    case "pull":
        v := g.Pull
        result, err := c.app.Projects.Pull(ctx, client.PullInput{IDs: v.IDs, Paths: v.Path, To: v.To, Into: v.Into, Checksum: v.Checksum, Concurrency: v.Concurrency, Policy: c.policy}, c.progress)
        if result.Single != nil {
            return *result.Single, err
        }
        if result.Batch != nil {
            return p.pulls(*result.Batch), err
        }
        return nil, err
    case "clean":
        if !g.Clean.DryRun && !g.Clean.Yes && c.policy.Batch {
            return nil, fault.Invalid("--yes", "missing_argument", "clean_confirmation_required")
        }
        preview, err := c.app.Projects.PrepareClean(ctx, client.CleanInput{IDs: g.Clean.IDs, PrepareRunner: !g.Clean.DryRun, Policy: c.policy})
        if err != nil || g.Clean.DryRun {
            return preview, err
        }
        if !g.Clean.Yes {
            fmt.Fprintln(p.err, p.locale.Text("clean_scope", map[string]any{"Count": len(preview.Runs)}))
            printClean(p.err, preview, p.locale)
            yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: p.locale.Text("clean_confirm"), Kind: interaction.ConfirmDefaultNo, AcceptLabel: p.locale.Text("yes"), RejectLabel: p.locale.Text("no")})
            if err != nil {
                return preview, err
            }
            if !yes {
                return preview, fault.New("CLEAN_CANCELED", "clean_canceled", nil)
            }
        }
        return c.app.Projects.ExecuteClean(ctx, preview, c.policy)
    case "exec":
        return c.exec(ctx)
    case "ssh":
        if p.json {
            return nil, fault.New("BAD_CONFIG", "interactive_json", nil)
        }
        if !c.options.Interactive {
            return nil, fault.New("BAD_CONFIG", "interactive_terminal", nil)
        }
        return nil, c.app.Machines.SSH(ctx, deref(g.Target), client.Streams{In: c.options.In, Out: p.out, Err: p.err})
    case "forward":
        if p.json {
            return nil, fault.New("BAD_CONFIG", "forward_json", nil)
        }
        return nil, c.app.Machines.Forward(ctx, deref(g.Target), g.Forward.Port, g.Forward.LocalPort, c.policy, p.err, c.progress)
    }
    if strings.HasPrefix(path, "cloud ") {
        return c.cloud(ctx, path)
    }
    return nil, fault.New("BAD_CONFIG", "invalid_command", nil)
}

func (c *invocation) progress(event client.Progress) {
    p := c.output
    if p.json {
        return
    }
    key := event.Stage + "_progress"
    switch event.Stage {
    case "sync_update":
        key = "sync_update_warning"
    case "run_accepted":
        key = "run_accepted"
    case "forward":
        fmt.Fprintln(p.err, p.locale.Text("forward_opening", map[string]any{"Target": event.Target, "Value": fmt.Sprintf("127.0.0.1:%d -> 127.0.0.1:%d", event.Count, c.grammar.Forward.Port)}))
        return
    default:
        if strings.HasPrefix(event.Stage, "run_") {
            key = "run_progress_" + strings.TrimPrefix(event.Stage, "run_")
        }
    }
    fmt.Fprintln(p.err, p.locale.Text(key, map[string]any{"Target": event.Target, "Count": event.Count, "ID": event.RunID}))
}

func (c *invocation) run(ctx context.Context) (any, error) {
    v := c.grammar.Run
    args, err := commandArgs(v.Argv, false)
    if err != nil {
        return nil, err
    }
    feedback := client.RunFeedback{Progress: c.progress}
    if !c.output.json {
        lastPhase := ""
        feedback.Log = func(v execution.View, kind string) error {
            if v.Phase != "" && v.Phase != lastPhase {
                lastPhase = v.Phase
                fmt.Fprintln(c.output.err, c.output.locale.Text("run_phase_"+v.Phase))
            }
            return writeLog(c.output.out, v)
        }
    }
    result, err := c.app.Projects.Run(ctx, client.RunInput{Target: c.grammar.Target, Wait: v.Wait, Args: args, Name: v.Name, Tags: v.Tag, DryRun: v.DryRun, Policy: c.policy}, feedback)
    if result.Preview != nil {
        return *result.Preview, err
    }
    if result.Run != nil {
        c.output.waited = result.Observed
        return *result.Run, err
    }
    return nil, err
}

type logAcknowledgement struct {
    RunID        string `json:"run_id"`
    Acknowledged bool   `json:"acknowledged"`
    Offset       int64  `json:"offset"`
    StateError   string `json:"state_error,omitempty"`
}

func (c *invocation) logs(ctx context.Context) (any, error) {
    v, p := c.grammar.Logs, c.output
    feedback := client.LogsFeedback{Emit: func(view execution.View, kind string) (bool, error) {
        if p.json {
            return false, p.stream(struct {
                Type string         `json:"type"`
                Run  execution.View `json:"run"`
            }{kind, view})
        }
        if err := writeLog(p.out, view); err != nil {
            return false, err
        }
        return c.options.TerminalOutput, nil
    }}
    if !p.json {
        feedback.Recovery = func(state string) { fmt.Fprintln(p.err, p.locale.Text("log_recovery_"+state)) }
    }
    result, err := c.app.Projects.Logs(ctx, client.LogsInput{ID: v.ID, Tail: v.Tail, Follow: v.Follow, Reconnect: v.Reconnect, Resume: v.Resume, ResumeFile: v.ResumeFile, AckCursor: v.AckCursor, RecoveryBudget: v.RecoveryBudget, Policy: c.policy}, feedback)
    if v.AckCursor != nil && result.View.ID != "" {
        return logAcknowledgement{RunID: result.View.ID, Acknowledged: result.Acknowledged, Offset: result.Offset, StateError: result.View.StateError}, err
    }
    if v.Follow {
        if err != nil && p.json {
            return struct {
                Type string         `json:"type"`
                Run  execution.View `json:"run"`
            }{"error", result.View}, err
        }
        return nil, err
    }
    if p.json {
        return result.View, err
    }
    if err == nil {
        err = writeLog(p.out, result.View)
        if result.View.Log != nil && result.View.Log.Truncated {
            fmt.Fprintln(p.err, p.locale.Text("logs_truncated"))
        }
    }
    return nil, err
}

func (c *invocation) exec(ctx context.Context) (any, error) {
    args, err := commandArgs(c.grammar.Exec.Argv, true)
    if err != nil {
        return nil, err
    }
    p := c.output
    streams := client.Streams{In: c.options.In, Out: p.out, Err: p.err}
    if p.json {
        lock := &sync.Mutex{}
        encoder := json.NewEncoder(p.out)
        streams.Out = &execOutput{encoder: encoder, lock: lock, stream: "stdout", writeErr: &p.writeErr}
        streams.Err = &execOutput{encoder: encoder, lock: lock, stream: "stderr", writeErr: &p.writeErr}
    }
    result, err := c.app.Machines.Exec(ctx, client.ExecInput{Target: deref(c.grammar.Target), CWD: c.grammar.Exec.CWD, Args: args, Policy: c.policy}, streams)
    if !p.json {
        return nil, err
    }
    end := execResult{Type: "end", Target: result.Target, ExecResult: result.ExecResult}
    if err != nil {
        end.Type = "error"
        return end, err
    }
    return end, nil
}

func commandArgs(args []string, required bool) ([]string, error) {
    if len(args) > 0 && args[0] == "--" {
        args = args[1:]
    } else if len(args) > 0 {
        return nil, fault.Invalid("--", "missing_argument", "run_command")
    }
    if required && len(args) == 0 {
        return nil, fault.Invalid("command", "missing_argument", "exec_command")
    }
    return args, nil
}
func writeLog(out io.Writer, v execution.View) error {
    if v.Log == nil {
        return nil
    }
    n, err := io.WriteString(out, v.Log.Text)
    if err == nil && n != len(v.Log.Text) {
        return io.ErrShortWrite
    }
    return err
}
func deref[T any](p *T) (v T) {
    if p != nil {
        return *p
    }
    return v
}

// complete is for operations that have no meaningful partial public result.
func complete[T any](result T, err error) (any, error) {
    if err != nil {
        return nil, err
    }
    return result, nil
}
