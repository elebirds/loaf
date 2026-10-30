package cli

import (
    "context"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
)

func (c *invocation) uxCRunArgs() (client.RunInput, error) {
    v := c.grammar.Run
    args, err := commandArgs(v.Argv, false)
    return client.RunInput{Target: c.grammar.Target, Wait: v.Wait, Args: args, Name: v.Name, Tags: v.Tag, Notify: v.Notify, DryRun: v.DryRun, Policy: c.policy}, err
}

// uxCRunCommand leaves complete calls and all machine-readable calls direct.
func (c *invocation) uxCRunCommand(ctx context.Context) (any, error) {
    in, err := c.uxCRunArgs()
    if err != nil {
        return nil, err
    }
    if !c.uxInteractive() || in.DryRun {
        return c.uxCRunInput(ctx, in)
    }
    required, err := c.app.Projects.RunRequirements(ctx, in)
    if err != nil {
        return nil, err
    }
    if !required.CommandMissing && !required.TargetMissing {
        return c.uxCRunInput(ctx, in)
    }
    return c.uxCReviewRun(ctx, in, required)
}

// uxCRun is the root-menu entry. It presents the outcome with navigation
// tied to the returned run ID, including preparing and uncertain submissions.
func (c *invocation) uxCRun(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    in, err := c.uxCRunArgs()
    if err != nil {
        return nil, err
    }
    data, cause := c.uxCRunDraft(ctx, in)
    if data == nil {
        return nil, cause
    }
    run, ok := data.(execution.View)
    if !ok {
        return data, cause
    }
    if ctx.Err() != nil {
        // A canceled menu cannot open another form. Retain the saved identity
        // visibly even though the root navigator is also unwinding.
        printRun(c.output.err, run, c.output.locale)
        printRunNext(c.output.err, run, c.output.locale, c.output.waited)
        return nil, cause
    }
    return nil, c.uxCRunOutcome(ctx, run, cause)
}

func (c *invocation) uxCRunDraft(ctx context.Context, in client.RunInput) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    required, err := c.app.Projects.RunRequirements(ctx, in)
    if err != nil {
        return nil, err
    }
    return c.uxCReviewRun(ctx, in, required)
}

func (c *invocation) uxCReviewRun(ctx context.Context, in client.RunInput, required client.RunRequirements) (any, error) {
    var err error
    if required.TargetMissing {
        target, err := c.uxAPickTarget(ctx, required.Target)
        if err != nil {
            return nil, err
        }
        in.Target = &target
    }
    if required.CommandMissing {
        in.Args, err = c.uxCEditCommand(ctx, in.Args)
        if err != nil {
            return nil, err
        }
    }
    var preview *execution.RunPreview
    for {
        if preview == nil {
            local := in
            local.DryRun = true
            result, err := c.app.Projects.Run(ctx, local, client.RunFeedback{})
            if err != nil {
                return nil, err
            }
            preview = result.Preview
        }
        l := c.output.locale
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_c_run_review"), c.uxCRunSummary(*preview), []interaction.Choice{
            {Label: l.Text("ux_c_submit"), Value: "submit"},
            {Label: l.Text("ux_c_details"), Value: "details"},
            {Label: l.Text("ux_c_edit"), Value: "edit"},
            {Label: l.Text("ux_core_back"), Value: "back"},
        }, "details")
        if err != nil || action == "back" {
            return nil, err
        }
        switch action {
        case "details":
            err = c.uxCRunDetails(ctx, *preview)
        case "edit":
            action, err = c.input.Select(ctx, l.Text("ux_c_edit"), []interaction.Choice{
                {Label: l.Text("ux_c_command_title"), Value: "command"},
                {Label: l.Text("ux_core_machine"), Value: "machine"},
                {Label: l.Text("ux_core_back"), Value: "back"},
            }, "back")
            if err == nil {
                switch action {
                case "command":
                    var args []string
                    args, err = c.uxCEditCommand(ctx, in.Args)
                    if err == nil {
                        in.Args, preview = args, nil
                    }
                case "machine":
                    var target string
                    target, err = c.uxAPickTarget(ctx, preview.Target)
                    if err == nil {
                        in.Target, preview = &target, nil
                    }
                }
            }
        case "submit":
            // Run revalidates the current files and performs exactly one Submit.
            // The local preview is not a frozen source snapshot or a readiness check.
            in.DryRun = false
            return c.uxCRunInput(ctx, in)
        }
        if err != nil && !errors.Is(err, interaction.ErrCanceled) {
            return nil, err
        }
    }
}

// uxCRunInput is the shared typed execution boundary for direct and guided run.
// Sparse progress does not read stdin; native authentication and the successful
// log-consumption sink retain exclusive terminal ownership throughout the call.
func (c *invocation) uxCRunInput(ctx context.Context, in client.RunInput) (any, error) {
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

func (c *invocation) uxCRunSummary(p execution.RunPreview) string {
    l := c.output.locale
    mode := "argv"
    if len(p.Args) == 3 && p.Args[0] == "/bin/sh" && p.Args[1] == "-c" {
        mode = "/bin/sh -c"
    }
    notice := l.Text("ux_c_notifications_none")
    if len(p.Notifications) > 0 {
        notice = strings.Join(p.Notifications, ", ")
    }
    return c.uxCSummaryLines(
        p.Target+" · "+mode+" · "+l.Text("ux_c_setup", map[string]any{"Value": booleanText(p.SetupPresent, l)}),
        fmt.Sprintf("%q", p.Args),
        l.Text("ux_c_sources", map[string]any{"Count": len(p.Source.Files), "Bytes": p.Source.Bytes, "Mode": p.Source.Policy.Mode}),
        l.Text("preview_outputs", map[string]any{"Paths": strings.Join(p.Outputs, ", ")}),
        l.Text("ux_c_wait_notify", map[string]any{"Wait": (time.Duration(p.WaitMillis) * time.Millisecond).String(), "Notify": notice}),
    )
}

func (c *invocation) uxCSummaryLines(lines ...string) string {
    width := max(20, min(96, interaction.Width(c.options.Err))-8)
    for i, line := range lines {
        line = strings.ReplaceAll(strings.ReplaceAll(line, "\n", "\\n"), "\r", "\\r")
        lines[i] = ansi.Truncate(ansi.Strip(line), width, "…")
    }
    return strings.Join(lines, "\n")
}

func (c *invocation) uxCRunDetails(ctx context.Context, preview execution.RunPreview) error {
    l := c.output.locale
    action, err := c.input.Select(ctx, l.Text("ux_c_details"), []interaction.Choice{
        {Label: l.Text("ux_c_execution_details"), Value: "execution"},
        {Label: l.Text("ux_c_source_details"), Value: "source"},
        {Label: l.Text("ux_core_back"), Value: "back"},
    }, "execution")
    if err != nil || action == "back" {
        return err
    }
    var body strings.Builder
    if action == "execution" {
        printPreview(&body, preview, l)
        fmt.Fprintln(&body, l.Text("ux_c_notify_detail", map[string]any{"Value": preview.Notifications}))
        if preview.SetupPresent {
            fmt.Fprintln(&body, l.Text("ux_c_setup_separate"))
        }
    } else {
        printSync(&body, client.SyncResult{DryRun: true, Policy: preview.Source.Policy, SelectedFiles: len(preview.Source.Files), SelectedBytes: preview.Source.Bytes, Files: preview.Source.Files, Excluded: preview.Source.Excluded}, l)
        fmt.Fprintln(&body, l.Text("ux_c_source_rules", map[string]any{"Value": fmt.Sprintf("%q", preview.Source.Policy.Exclude)}))
    }
    return c.uxShowDocument(ctx, interaction.Document{Title: l.Text("ux_c_details"), Body: body.String()})
}

func (c *invocation) uxCRunOutcome(ctx context.Context, run execution.View, cause error) error {
    l := c.output.locale
    var body strings.Builder
    printRun(&body, run, l)
    if cause != nil {
        failure := c.output.error(cause)
        fmt.Fprintln(&body, failure.Message)
        if failure.Hint != "" {
            fmt.Fprintln(&body, failure.Hint)
        }
    }
    printRunNext(&body, run, l, c.output.waited)
    summary := []string{run.ID + " · " + run.Target,
        l.Text("ux_d_submission") + ": " + c.uxDSubmission(run.Submission),
        l.Text("ux_d_execution") + ": " + localizedRunState(run.Status, l)}
    if run.ExitCode != nil {
        summary[2] += " · " + l.Text("run_exit", map[string]any{"Value": *run.ExitCode})
    }
    if run.Submission == "uncertain" || run.Submission == "preparing" {
        summary[1] = l.Text("run_submission_" + run.Submission)
    }
    if cause != nil {
        failure := c.output.error(cause)
        summary = append(summary, failure.Code+": "+failure.Message)
    }
    if run.StateError != "" {
        summary = append(summary, l.Text("run_state_save_failed", map[string]any{"Code": run.StateError}))
    }
    focus := "task"
    for {
        current := append([]string{}, summary...)
        availability := ""
        if run.CleanedAt != nil {
            availability = l.Text("clean_status_cleaned")
        } else if run.CleanupPending {
            availability = l.Text("clean_status_pending")
        }
        if availability != "" {
            current = append(current, availability)
        }
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_c_next"), c.uxCSummaryLines(current...), c.uxCRunOutcomeChoices(run), focus)
        if errors.Is(err, interaction.ErrCanceled) || action == "back" {
            return nil
        }
        if err != nil {
            return err
        }
        focus = action
        if action == "details" {
            err = c.uxShowDocument(ctx, interaction.Document{Title: l.Text("ux_core_result"), Body: strings.TrimSpace(body.String() + "\n" + availability)})
        } else {
            var data any
            data, err = c.uxNavigate(ctx, uxRoute{Kind: action, ID: run.ID})
            if !errors.Is(err, interaction.ErrCanceled) && (data != nil || err != nil) && ctx.Err() == nil {
                err = c.uxShowResult(ctx, data, err)
            }
        }
        if ctx.Err() != nil {
            return ctx.Err()
        }
        if err != nil && !errors.Is(err, interaction.ErrCanceled) {
            return err
        }
        // Child actions may clean the payload. Refresh only saved availability
        // for this exact ID; keep the original submission/observation result
        // rather than inferring fresh execution status from local history.
        if action != "details" {
            if saved, err := c.app.Projects.Inspect(ctx, run.ID); err == nil {
                run.CleanedAt, run.CleanupPending = saved.CleanedAt, saved.CleanupPending
            }
        }
    }
}

func (c *invocation) uxCRunOutcomeChoices(run execution.View) []interaction.Choice {
    choices := []interaction.Choice{}
    if c.uxRouteAvailable("task") {
        choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_c_open_task"), Value: "task"})
    }
    available := run.Submission != "preparing" && run.CleanedAt == nil && !run.CleanupPending
    if available && c.uxRouteAvailable("logs") {
        choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_c_open_logs"), Value: "logs"})
    }
    if available && c.uxRouteAvailable("outputs") {
        choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_d_outputs"), Value: "outputs"})
    }
    choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_details"), Value: "details"})
    choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_back"), Value: "back"})
    return choices
}

func uxCMissingTarget(err error) bool {
    var f *fault.Error
    return errors.As(err, &f) && (f.Message == "target_required" || f.Message == "target_unknown")
}
