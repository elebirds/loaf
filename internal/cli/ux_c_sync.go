package cli

import (
    "context"
    "errors"
    "fmt"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *invocation) uxCSyncArgs() client.SyncInput {
    v := c.grammar.Sync
    return client.SyncInput{Target: c.grammar.Target, DryRun: v.DryRun, Mode: v.Mode, RespectGitignore: v.RespectGitignore, Exclude: v.Exclude, Policy: c.policy}
}

// uxCSyncCommand preserves complete sync and target-free dry-run calls.
func (c *invocation) uxCSyncCommand(ctx context.Context) (any, error) {
    in := c.uxCSyncArgs()
    if c.uxInteractive() && !in.DryRun && in.Target == nil {
        _, err := c.app.Projects.SyncTarget(ctx, in.Target)
        if uxCMissingTarget(err) {
            return c.uxCSyncDraft(ctx, in)
        }
        if err != nil {
            return nil, err
        }
    }
    return c.uxCSyncInput(ctx, in)
}

// uxCSync starts with local source inspection. It neither needs a registered
// machine nor makes a connection until the user explicitly chooses transfer.
func (c *invocation) uxCSync(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    return c.uxCSyncDraft(ctx, c.uxCSyncArgs())
}

func (c *invocation) uxCSyncDraft(ctx context.Context, in client.SyncInput) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    local := in
    local.DryRun = true
    preview, err := c.app.Projects.Sync(ctx, local, nil)
    if err != nil {
        return nil, err
    }
    target, err := c.app.Projects.SyncTarget(ctx, in.Target)
    if err != nil && (in.Target != nil || !uxCMissingTarget(err)) {
        return nil, err
    }
    for {
        l := c.output.locale
        selected := target
        if selected == "" {
            selected = l.Text("ux_core_no_machine")
        }
        summary := c.uxCSummaryLines(
            selected,
            l.Text("ux_c_sources", map[string]any{"Count": preview.SelectedFiles, "Bytes": preview.SelectedBytes, "Mode": preview.Policy.Mode}),
            l.Text("ux_c_excluded", map[string]any{"Count": len(preview.Excluded), "Value": booleanText(preview.Policy.RespectGitignore, l)}),
        )
        if preview.Policy.Mode == "update" {
            summary += "\n" + c.uxCSummaryLines(l.Text("sync_update_warning"))
        }
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_c_sync_review"), summary, []interaction.Choice{
            {Label: l.Text("ux_c_sync_submit"), Value: "sync"},
            {Label: l.Text("ux_c_source_details"), Value: "details"},
            {Label: l.Text("ux_c_machine_change"), Value: "machine"},
            {Label: l.Text("ux_core_back"), Value: "back"},
        }, "details")
        if err != nil || action == "back" {
            return nil, err
        }
        switch action {
        case "details":
            var body strings.Builder
            printSync(&body, preview, l)
            fmt.Fprintln(&body, l.Text("ux_c_source_rules", map[string]any{"Value": fmt.Sprintf("%q", preview.Policy.Exclude)}))
            err = c.uxShowDocument(ctx, interaction.Document{Title: l.Text("ux_c_source_details"), Body: body.String()})
        case "machine":
            var next string
            next, err = c.uxAPickTarget(ctx, target)
            if err == nil {
                target, in.Target = next, &next
            }
        case "sync":
            if target == "" {
                var next string
                next, err = c.uxAPickTarget(ctx, "")
                if err == nil {
                    target, in.Target = next, &next
                }
                // Show the selected destination before a transfer is authorized.
                break
            }
            in.DryRun = false
            return c.uxCSyncInput(ctx, in)
        }
        if err != nil && !errors.Is(err, interaction.ErrCanceled) {
            return nil, err
        }
    }
}

func (c *invocation) uxCSyncInput(ctx context.Context, in client.SyncInput) (any, error) {
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
