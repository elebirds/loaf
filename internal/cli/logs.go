package cli

import (
    "context"
    "errors"
    "fmt"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

// logReceipt keeps presentation evidence only, never log content or retry
// policy. Projects.Logs remains the sole owner of consumption and checkpoints.
type logReceipt struct {
    ended, exhausted, consumerFailed bool
}

func (c *invocation) directLogs(ctx context.Context) (any, error) {
    v, p := c.grammar.Logs, c.output
    in := client.LogsInput{ID: v.ID, Tail: v.Tail, Follow: v.Follow, Reconnect: v.Reconnect, Resume: v.Resume, ResumeFile: v.ResumeFile, AckCursor: v.AckCursor, RecoveryBudget: v.RecoveryBudget, Policy: c.policy}
    result, receipt, err := c.readLogs(ctx, in)
    if v.AckCursor != nil && result.View.ID != "" {
        return logAcknowledgement{RunID: result.View.ID, Acknowledged: result.Acknowledged, Offset: result.Offset, StateError: result.View.StateError}, err
    }
    if v.Follow {
        if p.json {
            if err != nil {
                return struct {
                    Type string         `json:"type"`
                    Run  execution.View `json:"run"`
                }{"error", result.View}, err
            }
            return nil, nil
        }
        if err == nil {
            err = c.printLogFinished(result, receipt, true)
        } else if !logCanceled(ctx, err) {
            err = errors.Join(err, c.printLogOutcome(result))
            actions := logRecoveryActions(result, receipt, err, c.options.TerminalOutput)
            if infoErr := c.printLogRecovery(result, receipt, actions, v.ResumeFile != nil); infoErr != nil {
                err = errors.Join(err, infoErr)
            }
            if len(actions) > 0 {
                err = errors.Join(err, c.printLogCommand(actions[0]))
            }
        }
        return nil, err
    }
    if p.json {
        return result.View, err
    }
    return nil, err
}

func (c *invocation) readLogs(ctx context.Context, in client.LogsInput) (client.LogsResult, logReceipt, error) {
    p := c.output
    receipt := logReceipt{}
    started := false
    var progressErr error
    feedback := client.LogsFeedback{Emit: func(view execution.View, kind string) (bool, error) {
        if progressErr != nil {
            receipt.consumerFailed = true
            return false, progressErr
        }
        if p.json {
            err := p.stream(struct {
                Type string         `json:"type"`
                Run  execution.View `json:"run"`
            }{kind, view})
            receipt.consumerFailed = err != nil
            return false, err
        }
        if !started {
            started = true
            mode := "ux_e_read_tail"
            if in.Resume != nil {
                mode = "ux_e_read_cursor"
            } else if in.ResumeFile != nil {
                mode = "ux_e_read_file"
            }
            if _, err := fmt.Fprintln(p.err, p.locale.Text("ux_e_following", map[string]any{"ID": view.ID, "Target": view.Target, "Mode": p.locale.Text(mode)})); err != nil {
                receipt.consumerFailed = true
                return false, err
            }
        }
        if err := writeLog(p.out, view); err != nil {
            receipt.consumerFailed = true
            return false, err
        }
        receipt.ended = kind == "end"
        return c.options.TerminalOutput, nil
    }}
    feedback.Recovery = func(state string) {
        if state == "exhausted" {
            receipt.exhausted = true
            // The final result explains exhaustion once. The callback does not
            // report a reconnect count or a duration, so neither is invented.
            return
        }
        if !p.json && progressErr == nil && (state == "reconnecting" || state == "recovered") {
            _, progressErr = fmt.Fprintln(p.err, p.locale.Text("log_recovery_"+state))
        }
    }
    var result client.LogsResult
    err := c.input.WithNative(ctx, nil, func() error {
        var err error
        result, err = c.app.Projects.Logs(ctx, in, feedback)
        if err == nil && progressErr != nil {
            err = progressErr
        }
        if err == nil && !p.json && !in.Follow && in.AckCursor == nil {
            err = writeLog(p.out, result.View)
            if err == nil && result.View.Log != nil && result.View.Log.Truncated {
                _, err = fmt.Fprintln(p.err, p.locale.Text("logs_truncated"))
            }
        }
        return err
    })
    return result, receipt, err
}

func logCanceled(ctx context.Context, err error) bool {
    if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, interaction.ErrCanceled) {
        return true
    }
    var f *fault.Error
    return errors.As(err, &f) && (f.Code == "OBSERVATION_CANCELED" || f.Code == "OPERATION_CANCELED")
}

func logCursor(result client.LogsResult) string {
    v := result.View
    if v.Log != nil {
        cursor, err := wire.DecodeLogCursor(v.Log.Cursor)
        if err == nil && cursor.Matches(wire.Request{Version: v.Version, Root: v.RemoteDir, ProjectID: v.Ref.ProjectID, RunID: v.ID}) && cursor.Offset == v.Log.Offset {
            return v.Log.Cursor
        }
    }
    return ""
}

// logRecoveryActions describes a new human-authorized read, never a retry loop or a
// workload action. Only successfully consumed terminal output permits resume.
func logRecoveryActions(result client.LogsResult, receipt logReceipt, cause error, terminal bool) []uxNextAction {
    if cause == nil || result.View.ID == "" || result.View.StateError != "" || receipt.ended || receipt.consumerFailed || !terminal || logCanceled(context.Background(), cause) {
        return nil
    }
    var f *fault.Error
    if errors.As(cause, &f) && f.Code == "LOG_CHANGED" {
        return []uxNextAction{{Kind: "fresh_tail", Command: []string{"loaf", "logs", result.View.ID, "-f"}, RequiresConfirmation: true}}
    }
    token := logCursor(result)
    if token != "" && (ssh.IsTransientReadError(cause) || errors.As(cause, &f) && f.Code == "LOG_RECOVERY_EXHAUSTED") {
        return []uxNextAction{{Kind: "resume", Command: []string{"loaf", "logs", result.View.ID, "-f", "--resume", token}, RequiresConfirmation: true}}
    }
    return nil
}

func (c *invocation) printLogRecovery(result client.LogsResult, receipt logReceipt, actions []uxNextAction, checkpoint bool) error {
    p := c.output
    if receipt.exhausted {
        if _, err := fmt.Fprintln(p.err, p.locale.Text("ux_e_exhausted")); err != nil {
            return err
        }
    }
    if len(actions) == 0 {
        return nil
    }
    if actions[0].Kind == "resume" {
        if _, err := fmt.Fprintln(p.err, p.locale.Text("ux_e_cursor_kept", map[string]any{"Offset": result.View.Log.Offset})); err != nil {
            return err
        }
        if checkpoint {
            if _, err := fmt.Fprintln(p.err, p.locale.Text("ux_e_cursor_only")); err != nil {
                return err
            }
        }
    }
    return nil
}

func (c *invocation) printLogCommand(action uxNextAction) error {
    p := c.output
    display, err := uxJCommandDisplay(action.Command)
    if err != nil {
        return err
    }
    _, err = fmt.Fprintln(p.err, p.locale.Text("ux_e_"+action.Kind)+":\n"+display)
    return err
}

func (c *invocation) printLogFinished(result client.LogsResult, receipt logReceipt, follow bool) error {
    p := c.output
    key := "ux_e_tail_done"
    if follow {
        key = "ux_e_eof"
        if receipt.ended {
            key = "ux_e_follow_done"
        }
    }
    if _, err := fmt.Fprintln(p.err, p.locale.Text(key)); err != nil {
        return err
    }
    return c.printLogOutcome(result)
}

func (c *invocation) printLogOutcome(result client.LogsResult) error {
    p, v := c.output, result.View
    // An execution outcome is reported independently of stream EOF. Never pass
    // the log-bearing View to printRun, which would print the consumed tail.
    if wire.Terminal(v.Status) {
        text := p.locale.Text("ux_e_outcome", map[string]any{"ID": v.ID, "Outcome": localizedRunState(v.Status, p.locale)})
        if v.ExitCode != nil {
            text += " · " + p.locale.Text("run_exit", map[string]any{"Value": *v.ExitCode})
        }
        _, err := fmt.Fprintln(p.err, text)
        return err
    }
    return nil
}
