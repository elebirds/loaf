package cli

import (
    "context"
    "errors"
    "fmt"
    "strconv"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/wire"
)

// uxELogs observes the explicit selection supplied by task navigation. All input
// is collected before native stream ownership; only an explicit recovery choice
// can start another observation of that same run.
func (c *invocation) uxELogs(ctx context.Context, id string) (any, error) {
    if err := (uxRoute{Kind: "logs", ID: id}).validate(); err != nil {
        return nil, err
    }
    if !c.uxInteractive() || c.output.json || !c.options.Interactive {
        return nil, interaction.ErrNonInteractive
    }
    details, err := c.app.Projects.Inspect(ctx, id)
    if err != nil {
        return nil, err
    }
    l := c.output.locale
    summary := l.Text("ux_e_context", map[string]any{"ID": details.ID, "Target": details.Target})
    mode, err := c.input.SelectWithSummary(ctx, l.Text("ux_e_title"), summary, []interaction.Choice{
        {Label: l.Text("ux_e_recent"), Value: "recent"},
        {Label: l.Text("ux_e_follow"), Value: "follow"},
        {Label: l.Text("ux_e_advanced"), Value: "advanced"},
        {Label: l.Text("ux_core_back"), Value: "back"},
    }, "recent")
    if err != nil || mode == "back" {
        return nil, err
    }
    in := client.LogsInput{ID: id, Follow: mode != "recent", Policy: c.policy}
    if mode == "advanced" {
        if err := c.uxEAdvancedLogs(ctx, &in); err != nil {
            return nil, err
        }
    } else if err := c.uxELogTail(ctx, &in, false); err != nil {
        return nil, err
    }
    for {
        result, receipt, cause := c.uxEReadLogs(ctx, in)
        if uxELogCanceled(ctx, cause) {
            return nil, cause
        }
        if cause == nil {
            return nil, c.uxELogFinished(result, receipt, in.Follow)
        }
        actions := uxELogActions(result, receipt, cause, c.options.TerminalOutput)
        if len(actions) == 0 {
            // The caller renders the original error, including its stable code
            // and stage. No form is reopened after consumption/state failures.
            return nil, errors.Join(cause, c.uxELogOutcome(result))
        }
        failure := c.output.error(cause)
        if _, err := fmt.Fprintln(c.output.err, failure.Code+": "+failure.Message); err != nil {
            return nil, err
        }
        if err := c.uxELogRecoveryInfo(result, receipt, actions, in.ResumeFile != nil); err != nil {
            return nil, err
        }
        // A plain prompt leaves the already-written log untouched. It shares
        // the same input owner and runs only after WithNative has returned.
        choice, err := c.input.Plain().Text(ctx, interaction.TextPrompt{
            Title: l.Text("ux_e_recovery_choice") + " ", MaxBytes: 1,
            Description: "1. " + l.Text("ux_e_keep") + "\n2. " + l.Text("ux_e_"+actions[0].Kind),
            Validate: func(value string) error {
                if value != "" && value != "1" && value != "2" {
                    return errors.New(l.Text("ux_core_choice_invalid"))
                }
                return nil
            },
        })
        if err != nil {
            return nil, err
        }
        if choice != "2" {
            return nil, c.uxELogCommand(actions[0])
        }
        // A new explicit observation never inherits a checkpoint file, spent
        // recovery budget or old tail when resuming a consumed cursor.
        in = client.LogsInput{ID: id, Follow: true, Policy: c.policy}
        if actions[0].Kind == "resume" {
            token := result.View.Log.Cursor
            in.Resume = &token
        } else if err := c.uxELogTail(ctx, &in, true); err != nil {
            return nil, err
        }
    }
}

func (c *invocation) uxELogTail(ctx context.Context, in *client.LogsInput, plain bool) error {
    l := c.output.locale
    prompt := interaction.TextPrompt{Title: l.Text("ux_e_tail"), Description: l.Text("ux_e_tail_help"), MaxBytes: 5,
        Validate: func(value string) error {
            if value == "" {
                return nil
            }
            n, err := strconv.Atoi(value)
            if err != nil || n < 0 || n > 10000 {
                return errors.New(l.Text("invalid_tail"))
            }
            return nil
        }}
    var value string
    var err error
    if plain {
        value, err = c.input.Plain().Text(ctx, prompt)
    } else {
        value, err = c.input.Text(ctx, prompt)
    }
    if err == nil && value != "" {
        n, _ := strconv.Atoi(value)
        in.Tail = &n
    }
    return err
}

func (c *invocation) uxEAdvancedLogs(ctx context.Context, in *client.LogsInput) error {
    l := c.output.locale
    source, err := c.input.Select(ctx, l.Text("ux_e_start"), []interaction.Choice{
        {Label: l.Text("ux_e_recent"), Value: "tail"},
        {Label: l.Text("ux_e_cursor"), Value: "cursor"},
        {Label: l.Text("ux_e_file"), Value: "file"},
    }, "tail")
    if err != nil {
        return err
    }
    switch source {
    case "tail":
        if err := c.uxELogTail(ctx, in, false); err != nil {
            return err
        }
    case "cursor":
        value, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_e_cursor"), MaxBytes: 8192, Validate: func(value string) error {
            cursor, err := wire.DecodeLogCursor(value)
            if err != nil || cursor.RunID != in.ID {
                return errors.New(l.Text("log_cursor_invalid"))
            }
            return nil
        }})
        if err != nil {
            return err
        }
        in.Resume = &value
    case "file":
        value, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_e_file"), Description: l.Text("ux_e_file_help"), MaxBytes: 4096, Validate: func(value string) error {
            if strings.TrimSpace(value) == "" {
                return errors.New(l.Text("log_checkpoint_invalid"))
            }
            return nil
        }})
        if err != nil {
            return err
        }
        in.ResumeFile = &value
    }
    choices := []interaction.Choice{}
    initial := "once"
    if source == "tail" {
        choices = append(choices, interaction.Choice{Label: l.Text("ux_e_single"), Value: "single"})
        initial = "single"
    }
    choices = append(choices, interaction.Choice{Label: l.Text("ux_e_once"), Value: "once"}, interaction.Choice{Label: l.Text("ux_e_budget"), Value: "budget"})
    recovery, err := c.input.Select(ctx, l.Text("ux_e_recovery"), choices, initial)
    if err != nil {
        return err
    }
    in.Reconnect = recovery == "once"
    if recovery != "budget" {
        return nil
    }
    value, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_e_budget"), Description: l.Text("ux_e_budget_help"), MaxBytes: 32, Validate: func(value string) error {
        duration, err := time.ParseDuration(value)
        if err != nil || duration < 0 || duration > 24*time.Hour {
            return errors.New(l.Text("log_recovery_budget"))
        }
        return nil
    }})
    if err != nil {
        return err
    }
    budget, _ := time.ParseDuration(value)
    in.RecoveryBudget = &budget
    return nil
}
