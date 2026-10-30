package cli

import (
    "context"
    "fmt"
    "io"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
)

func (c *invocation) abandon(ctx context.Context) (any, error) {
    v := c.grammar.Abandon
    if !v.DryRun && !v.Yes && !c.interactiveInput() {
        return nil, fault.Invalid("--yes", "missing_argument", "abandon_confirmation_required")
    }
    preview, err := c.app.Projects.PrepareAbandon(ctx, client.AbandonInput{IDs: v.IDs, PrepareRunner: !v.DryRun, Policy: c.policy})
    if err != nil || v.DryRun {
        return preview, err
    }
    if !v.Yes {
        printAbandon(c.output.err, preview, c.output.locale)
        yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: c.output.locale.Text("abandon_confirm"), Kind: interaction.ConfirmDefaultNo, AcceptLabel: c.output.locale.Text("yes"), RejectLabel: c.output.locale.Text("no")})
        if err != nil {
            return preview, err
        }
        if !yes {
            return preview, fault.New("ABANDON_CANCELED", "abandon_canceled", nil)
        }
    }
    return c.app.Projects.ExecuteAbandon(ctx, preview, c.policy)
}

func printAbandon(out io.Writer, batch client.AbandonBatch, l *i18n.Localizer) {
    for _, row := range batch.Runs {
        state := "abandon_preview"
        if row.CleanedAt != nil {
            state = "abandon_done"
        } else if row.NoStartAt != nil {
            state = "abandon_fenced"
        } else if row.ErrorCode != "" {
            state = "abandon_incomplete"
        }
        fmt.Fprintln(out, l.Text(state, map[string]any{"ID": row.Run.ID, "Target": row.Run.Target, "Path": row.Path}))
        if row.ErrorCode != "" {
            fmt.Fprintln(out, "  "+row.ErrorCode)
        }
        if row.StateError != "" {
            fmt.Fprintln(out, l.Text("run_state_save_failed", map[string]any{"Code": row.StateError}))
        }
    }
    if batch.DryRun {
        fmt.Fprintln(out, l.Text("abandon_preview_note"))
    }
}
