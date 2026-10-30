package cli

import (
    "context"
    "fmt"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
)

func (c *invocation) runInput() (client.RunInput, error) {
    v := c.grammar.Run
    args, err := commandArgs(v.Argv, false)
    return client.RunInput{Target: c.grammar.Target, Wait: v.Wait, Args: args, Name: v.Name, Tags: v.Tag, Notify: v.Notify, DryRun: v.DryRun, Policy: c.policy}, err
}

// submitRun adapts the command directly to the shared project workflow.

func (c *invocation) submitRun(ctx context.Context, in client.RunInput) (any, error) {
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
    var result client.RunResult
    err := c.input.WithNative(ctx, nil, func() error {
        var err error
        result, err = c.app.Projects.Run(ctx, in, feedback)
        return err
    })
    if result.Preview != nil {
        return *result.Preview, err
    }
    if result.Run != nil {
        c.output.waited = result.Observed
        return *result.Run, err
    }
    return nil, err
}

func (c *invocation) syncInput() client.SyncInput {
    v := c.grammar.Sync
    return client.SyncInput{Target: c.grammar.Target, DryRun: v.DryRun, Mode: v.Mode, RespectGitignore: v.RespectGitignore, Exclude: v.Exclude, Policy: c.policy}
}

// syncProject preserves complete sync and target-free dry-run calls.

func (c *invocation) syncProject(ctx context.Context, in client.SyncInput) (any, error) {
    var result client.SyncResult
    var err error
    if in.DryRun {
        // The local preview never owns native input; let Sync preserve its
        // existing cancellation code without a new presentation prerequisite.
        result, err = c.app.Projects.Sync(ctx, in, nil)
    } else {
        entered := false
        err = c.input.WithNative(ctx, nil, func() error {
            entered = true
            var err error
            result, err = c.app.Projects.Sync(ctx, in, c.progress)
            return err
        })
        if !entered && err != nil && ctx.Err() != nil {
            // Input ownership can be canceled before the use case is called.
            // No transfer happened; retain the direct sync cancellation contract.
            canceled := fault.Wrap("SYNC_CANCELED", "sync_canceled", ctx.Err())
            canceled.Hint = "sync_retry"
            err = canceled
        }
    }
    if err != nil {
        return nil, err
    }
    return result, nil
}
