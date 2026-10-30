package cli

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "runtime/debug"
    "strings"
    "sync"
    "sync/atomic"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
)

type invocation struct {
    ui              bool
    editAcquisition *atomic.Bool
    app             *client.App
    options         Options
    grammar         *grammar
    output          *presenter
    input           *interaction.InputSession
    policy          client.CallPolicy
}

// dispatch only adapts parsed input and presentation callbacks to application calls.
func (c *invocation) dispatch(ctx context.Context, path string) (any, error) {
    g, p := c.grammar, c.output
    switch path {
    case "version":
        info, _ := debug.ReadBuildInfo()
        return versionData(info), nil
    case "machine", "machine add", "machine use", "machine list", "machine show", "machine remove":
        return c.machine(ctx, path)
    case "check":
        return c.check(ctx)
    case "sync":
        return c.syncProject(ctx, c.syncInput())
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
        if !g.Clean.DryRun && !g.Clean.Yes && !c.interactiveInput() {
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
    case "abandon":
        return c.abandon(ctx)
    case "exec":
        return c.exec(ctx)
    case "ssh":
        if p.json {
            return nil, fault.New("BAD_CONFIG", "interactive_json", nil)
        }
        if !c.options.Interactive {
            return nil, fault.New("BAD_CONFIG", "interactive_terminal", nil)
        }
        return nil, c.input.WithNative(ctx, nil, func() error {
            return c.app.Machines.SSH(ctx, deref(g.Target), client.Streams{In: c.options.In, Out: p.out, Err: p.err})
        })
    case "forward":
        if p.json {
            return nil, fault.New("BAD_CONFIG", "forward_json", nil)
        }
        return nil, c.input.WithNative(ctx, nil, func() error {
            return c.app.Machines.Forward(ctx, deref(g.Target), g.Forward.Port, g.Forward.LocalPort, c.policy, p.err, c.progress)
        })
    }
    if strings.HasPrefix(path, "cloud ") {
        return c.cloud(ctx, path)
    }
    if path == "queue" || strings.HasPrefix(path, "queue ") {
        return c.queue(ctx, path)
    }
    if path == "events" || strings.HasPrefix(path, "events ") || path == "notify" || strings.HasPrefix(path, "notify ") {
        return c.async(ctx, path)
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
    case "sync":
        key = "sync_start"
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
    in, err := c.runInput()
    if err != nil {
        return nil, err
    }
    return c.submitRun(ctx, in)
}

type logAcknowledgement struct {
    RunID        string `json:"run_id"`
    Acknowledged bool   `json:"acknowledged"`
    Offset       int64  `json:"offset"`
    StateError   string `json:"state_error,omitempty"`
}

func (c *invocation) logs(ctx context.Context) (any, error) {
    return c.directLogs(ctx)
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
    var result client.ExecResult
    err = c.input.WithNative(ctx, nil, func() error {
        var callErr error
        result, callErr = c.app.Machines.Exec(ctx, client.ExecInput{Target: deref(c.grammar.Target), CWD: c.grammar.Exec.CWD, Args: args, Policy: c.policy}, streams)
        return callErr
    })
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
