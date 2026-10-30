package cli

import (
    "context"
    "errors"
    "fmt"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
)

// uxGClean reviews explicit IDs or D's local selection without preparing a helper.
func (c *invocation) uxGClean(ctx context.Context, ids []string) (any, error) {
    return c.uxGCleanFlow(ctx, ids, c.uxDPickRuns)
}

func (c *invocation) uxGCleanFlow(ctx context.Context, ids []string, pick func(context.Context, bool) ([]string, error)) (any, error) {
    if !c.uxInteractive() || c.output.json {
        return nil, interaction.ErrNonInteractive
    }
    ids = append([]string(nil), ids...)
    if len(ids) == 0 {
        if pick == nil {
            return nil, fault.New("BAD_CONFIG", "argument_count", map[string]any{"Command": "loaf clean"})
        }
        selected, err := pick(ctx, true)
        if err != nil || len(selected) == 0 {
            return nil, err
        }
        ids = append([]string(nil), selected...)
    }
    l := c.output.locale
    for {
        var preview client.CleanBatch
        err := c.input.WithNative(ctx, nil, func() error {
            var err error
            preview, err = c.app.Projects.PrepareClean(ctx, client.CleanInput{IDs: ids, PrepareRunner: false, Policy: c.policy})
            return err
        })
        if ctx.Err() != nil || len(preview.Runs) == 0 {
            return preview, err
        }
        // Keep every failed row. The selector's cached status is not eligibility.
        if err := c.uxShowDocument(ctx, uxGCleanDocument(preview, l)); err != nil {
            return nil, err
        }
        for {
            ready := uxGCleanReady(preview)
            choices := []interaction.Choice{}
            if ready {
                choices = append(choices, interaction.Choice{Label: l.Text("ux_g_delete"), Value: "delete"})
            }
            if c.uxRouteAvailable("outputs") && len(uxGDownloadIDs(preview)) > 0 {
                choices = append(choices, interaction.Choice{Label: l.Text("ux_g_download_first"), Value: "download"})
            }
            if pick != nil {
                choices = append(choices, interaction.Choice{Label: l.Text("ux_g_change_selection"), Value: "select"})
            }
            choices = append(choices,
                interaction.Choice{Label: l.Text("ux_g_review"), Value: "review"},
                interaction.Choice{Label: l.Text("ux_g_refresh"), Value: "refresh"},
                interaction.Choice{Label: l.Text("ux_g_cancel"), Value: "cancel"})
            summary := l.Text("ux_g_selection", map[string]any{"Count": len(preview.Runs)})
            if !ready {
                summary += "\n" + l.Text("ux_g_blocked_batch")
            }
            action, err := c.input.SelectWithSummary(ctx, l.Text("ux_g_title"), summary, choices, "cancel")
            if err != nil || action == "cancel" {
                return nil, err
            }
            switch action {
            case "review":
                if err := c.uxShowDocument(ctx, uxGCleanDocument(preview, l)); err != nil {
                    return nil, err
                }
                continue
            case "download":
                if err := c.uxGDownloadFirst(ctx, preview); err != nil {
                    return nil, err
                }
                // Download is a separate user action; coming back still requires
                // a fresh preview and a new, default-no deletion confirmation.
            case "select":
                selected, err := pick(ctx, true)
                if errors.Is(err, interaction.ErrCanceled) || err == nil && len(selected) == 0 {
                    continue
                }
                if err != nil {
                    return nil, err
                }
                ids = append([]string(nil), selected...)
            case "delete":
                yes, err := c.input.Confirm(ctx, interaction.Confirmation{
                    Title:       l.Text("ux_g_confirm", map[string]any{"Count": len(preview.Runs)}),
                    Description: l.Text("ux_g_scope") + "\n" + l.Text("ux_g_retained"),
                    Kind:        interaction.ConfirmDefaultNo, AcceptLabel: l.Text("ux_g_delete"), RejectLabel: l.Text("ux_g_keep"),
                })
                if err != nil {
                    return nil, err
                }
                if !yes {
                    _, err := fmt.Fprintln(c.output.err, l.Text("clean_canceled"))
                    return nil, err
                }
                var result client.CleanBatch
                cause := c.input.WithNative(ctx, nil, func() error {
                    var err error
                    result, err = c.app.Projects.ExecuteClean(ctx, preview, c.policy)
                    return err
                })
                // Preserve all per-ID facts even when cancellation makes another
                // form impossible. No remaining row is presented as successful.
                if ctx.Err() != nil {
                    if len(result.Runs) == 0 {
                        result = uxGNotAttempted(preview)
                    }
                    writeErr := c.uxGPrintSummary(result)
                    return nil, errors.Join(cause, ctx.Err(), writeErr)
                }
                if len(result.Runs) == 0 {
                    return nil, cause
                }
                displayErr := c.uxShowDocument(ctx, uxGCleanDocument(result, l))
                if err := errors.Join(displayErr, c.uxGPrintSummary(result)); err != nil {
                    return nil, err
                }
                ids = uxGRecoveryIDs(result)
                if len(ids) == 0 {
                    return nil, nil
                }
                action, err := c.input.Select(ctx, l.Text("ux_g_recovery"), []interaction.Choice{
                    {Label: l.Text("ux_g_review_incomplete"), Value: "review"},
                    {Label: l.Text("ux_core_back"), Value: "back"},
                }, "back")
                if err != nil || action == "back" {
                    return nil, err
                }
            }
            break
        }
    }
}

func uxGCleanReady(batch client.CleanBatch) bool {
    if len(batch.Runs) == 0 {
        return false
    }
    for _, item := range batch.Runs {
        if !item.Eligible || item.ErrorCode != "" {
            return false
        }
    }
    return true
}

func uxGNotAttempted(preview client.CleanBatch) client.CleanBatch {
    result := client.CleanBatch{Runs: append([]execution.CleanResult(nil), preview.Runs...)}
    for i := range result.Runs {
        result.Runs[i].ErrorCode = "CLEAN_NOT_ATTEMPTED"
    }
    return result
}

func uxGCleanDocument(batch client.CleanBatch, l *i18n.Localizer) interaction.Document {
    title := "ux_g_preview_title"
    if !batch.DryRun {
        title = "ux_g_result_title"
    }
    blocks := []string{}
    for _, item := range batch.Runs {
        state, reason := uxGCleanState(item, batch.DryRun, l)
        lines := []string{item.Run.ID + " · " + state}
        if item.Run.Target != "" {
            lines = append(lines, l.Text("ux_g_machine", map[string]any{"Target": item.Run.Target}))
        }
        if item.Path != "" {
            lines = append(lines, l.Text("ux_g_path", map[string]any{"Path": item.Path}))
        }
        if reason != "" {
            lines = append(lines, reason)
        }
        blocks = append(blocks, strings.Join(lines, "\n"))
    }
    footer := ""
    if !batch.DryRun {
        footer = l.Text("ux_g_retained")
    }
    return interaction.Document{Title: l.Text(title, map[string]any{"Count": len(batch.Runs)}), Body: strings.Join(blocks, "\n\n"), Footer: footer}
}

func uxGCleanState(item execution.CleanResult, preview bool, l *i18n.Localizer) (string, string) {
    if item.ErrorCode == "CLEAN_NOT_ATTEMPTED" {
        reason := l.Text("ux_g_not_attempted_reason")
        if item.Run.CleanupPending {
            reason += " " + l.Text("clean_unconfirmed")
        }
        return l.Text("ux_g_not_attempted"), reason
    }
    if item.Cleaned {
        return l.Text("clean_status_cleaned"), l.Text("clean_run_cleaned")
    }
    if item.Run.CleanupPending || item.ErrorCode == "CLEAN_UNCONFIRMED" {
        return l.Text("clean_status_pending"), l.Text("clean_unconfirmed")
    }
    if item.ErrorCode != "" {
        key := "clean_failed"
        switch item.ErrorCode {
        case "CLEAN_NOT_TERMINAL":
            key = "clean_not_terminal"
        case "CLEAN_NOT_CONFIRMED":
            key = "clean_not_confirmed"
        case "CLEAN_BUSY":
            key = "ux_g_busy"
        case "CLEAN_UNSAFE", "UNSAFE_PATH", "PROJECT_MISMATCH", "BAD_FORMAT":
            key = "clean_unsafe"
        case "RUN_NOT_FOUND":
            key = "ux_g_not_found"
        case "VERSION_MISMATCH":
            key = "ux_g_version"
        case "CLEAN_CANCELED":
            key = "clean_canceled"
        }
        return l.Text("ux_g_failed"), l.Text(key) + " (" + item.ErrorCode + ")"
    }
    if preview && item.Eligible {
        return l.Text("ux_g_eligible"), l.Text("ux_g_terminal", map[string]any{"Value": localizedRunState(item.Run.Status, l)})
    }
    return l.Text("ux_g_failed"), l.Text("clean_not_terminal")
}

func (c *invocation) uxGPrintSummary(batch client.CleanBatch) error {
    l := c.output.locale
    var summary strings.Builder
    fmt.Fprintln(&summary, l.Text("ux_g_result_title", map[string]any{"Count": len(batch.Runs)}))
    for _, item := range batch.Runs {
        state, _ := uxGCleanState(item, false, l)
        if item.ErrorCode != "" {
            state += " (" + item.ErrorCode + ")"
        }
        if item.ErrorCode == "CLEAN_NOT_ATTEMPTED" && item.Run.CleanupPending {
            state += " · " + l.Text("clean_status_pending")
        }
        fmt.Fprintf(&summary, "%s: %s\n", item.Run.ID, state)
    }
    fmt.Fprintln(&summary, l.Text("ux_g_retained"))
    _, err := fmt.Fprint(c.output.err, summary.String())
    return err
}

func uxGRecoveryIDs(batch client.CleanBatch) []string {
    ids := []string{}
    for _, item := range batch.Runs {
        if !item.Cleaned {
            ids = append(ids, item.Run.ID)
        }
    }
    return ids
}

// uxGNextActions carries explicit identities and never supplies --yes or executes
// a retry. Inspection remains available even if cleanup cannot be confirmed.
func uxGNextActions(batch client.CleanBatch) []uxNextAction {
    actions := []uxNextAction{}
    for _, item := range batch.Runs {
        actions = append(actions, uxNextAction{Kind: "inspect", Command: []string{"loaf", "inspect", item.Run.ID}})
    }
    if ids := uxGRecoveryIDs(batch); !batch.DryRun && len(ids) > 0 {
        actions = append(actions, uxNextAction{Kind: "clean", Command: append([]string{"loaf", "clean"}, ids...), RequiresConfirmation: true})
    }
    return actions
}

func uxGDownloadIDs(batch client.CleanBatch) []string {
    ids := []string{}
    for _, item := range batch.Runs {
        if item.Ref.ID != "" && !item.Cleaned && !item.Run.CleanupPending {
            ids = append(ids, item.Run.ID)
        }
    }
    return ids
}

func (c *invocation) uxGDownloadFirst(ctx context.Context, batch client.CleanBatch) error {
    choices := []interaction.Choice{}
    for _, id := range uxGDownloadIDs(batch) {
        choices = append(choices, interaction.Choice{Label: id, Value: id})
    }
    choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_back"), Value: "back"})
    id, err := c.input.Select(ctx, c.output.locale.Text("ux_g_download_first"), choices, "back")
    if err != nil || id == "back" {
        return err
    }
    data, cause := c.uxNavigate(ctx, uxRoute{Kind: "outputs", ID: id})
    if errors.Is(cause, interaction.ErrCanceled) {
        return nil
    }
    if data != nil || cause != nil {
        return c.uxShowResult(ctx, data, cause)
    }
    return nil
}
