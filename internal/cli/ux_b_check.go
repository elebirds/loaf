package cli

import (
    "context"
    "errors"
    "fmt"
    "io"
    "slices"
    "strconv"
    "strings"
    "unicode"

    "github.com/charmbracelet/x/ansi"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
)

// uxBDirectCheck preserves the finite command's typed result and fault contract.
// Only the menu entry chooses machines and pages through the observation.
func (c *invocation) uxBDirectCheck(ctx context.Context) (any, error) {
    v := c.grammar.Check
    var result client.CheckOutcome
    err := c.input.WithNative(ctx, nil, func() error {
        var err error
        result, err = c.app.Machines.Check(ctx, client.CheckInput{Target: c.grammar.Target, Targets: v.Targets.Values, Timeout: v.Timeout, Runner: v.Runner, Report: v.Report, Policy: c.policy})
        return err
    })
    if result.Single != nil {
        return c.output.check(*result.Single), err
    }
    if result.Batch != nil {
        return c.output.checks(*result.Batch), err
    }
    return nil, err
}

// uxBCheck accepts the grammar's check defaults. A machine detail supplies Target;
// without one the check-specific multi-select never changes the project choice.
func (c *invocation) uxBCheck(ctx context.Context) (any, error) {
    if !c.uxInteractive() || c.output.json {
        return nil, interaction.ErrNonInteractive
    }
    v := c.grammar.Check
    in := client.CheckInput{Target: c.grammar.Target, Targets: v.Targets.Values, Timeout: v.Timeout, Runner: v.Runner, Report: v.Report, Policy: c.policy}
    if in.Target == nil && in.Targets == nil {
        targets, err := c.app.Machines.Targets(ctx, "")
        if err != nil {
            return nil, err
        }
        if len(targets.Targets) == 0 {
            return nil, fault.New("BAD_CONFIG", "target_required", nil)
        }
        field := &interaction.Field{Title: c.output.locale.Text("ux_b_machines"), Multiple: true}
        for _, row := range targets.Targets {
            field.Choices = append(field.Choices, interaction.Choice{Label: row.Name, Value: row.Name})
            if row.Selected {
                field.Values = []string{row.Name}
            }
        }
        field.ValidateValues = func(values []string) error {
            if len(values) < 1 || len(values) > 32 {
                return errors.New(c.output.locale.Text("check_batch_flags"))
            }
            return nil
        }
        if err := c.input.Page(ctx, c.output.locale.Text("ux_b_check"), field); err != nil {
            return nil, err
        }
        // The plain selector accepts numbered lists; retain set semantics here.
        in.Targets = []string{}
        for _, name := range field.Values {
            if !slices.Contains(in.Targets, name) {
                in.Targets = append(in.Targets, name)
            }
        }
    }
    var outcome client.CheckOutcome
    err := c.input.WithNative(ctx, nil, func() error {
        count := max(1, len(in.Targets))
        if _, err := fmt.Fprintln(c.output.err, c.output.locale.Text("ux_b_checking", map[string]any{"Count": count, "Value": in.Timeout.String()})); err != nil {
            return err
        }
        var cause error
        outcome, cause = c.app.Machines.Check(ctx, in)
        return cause
    })
    if outcome.Single == nil && outcome.Batch == nil {
        return nil, err
    }
    // Cancellation must not reopen a form or discard already observed rows.
    if ctx.Err() != nil || errors.Is(err, context.Canceled) {
        if outcome.Single != nil {
            uxBPrintCheck(c.output.err, c.output.check(*outcome.Single), c.output.locale)
        } else {
            uxBPrintCheckBatch(c.output.err, c.output.checks(*outcome.Batch), c.output.locale)
        }
        return nil, err
    }
    if outcome.Single != nil {
        if showErr := c.uxBShowCheck(ctx, *outcome.Single); showErr != nil {
            return nil, showErr
        }
    } else if showErr := c.uxBShowChecks(ctx, *outcome.Batch); showErr != nil {
        return nil, showErr
    }
    // A report-write failure is independent of the observed connection results.
    var problem *fault.Error
    if errors.As(err, &problem) && problem.Code == "REPORT_FAILED" {
        return nil, err
    }
    return nil, nil
}

func (c *invocation) uxBShowChecks(ctx context.Context, batch client.CheckBatch) error {
    l := c.output.locale
    summary := uxBCoverage(c.output.checks(batch), l)
    for {
        choices := make([]interaction.Choice, 0, len(batch.Checks)+1)
        for i, row := range batch.Checks {
            choices = append(choices, interaction.Choice{Label: row.Target + " — " + uxBCheckState(c.output.check(row), l), Value: strconv.Itoa(i)})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_core_back"), Value: "back"})
        selected, err := c.input.SelectWithSummary(ctx, l.Text("ux_b_check"), summary, choices, "back")
        if err != nil {
            return err
        }
        if selected == "back" {
            _, err := fmt.Fprintln(c.output.err, summary)
            return err
        }
        index, err := strconv.Atoi(selected)
        if err != nil || index < 0 || index >= len(batch.Checks) {
            return interaction.ErrInvalidChoice
        }
        if err := c.uxBShowCheck(ctx, batch.Checks[index]); err != nil {
            return err
        }
    }
}

func (c *invocation) uxBShowCheck(ctx context.Context, result client.CheckResult) error {
    l := c.output.locale
    row := c.output.check(result)
    summary := uxBCheckState(row, l)
    if row.Error != nil {
        summary += " · " + row.Error.Code + "\n" + uxBFailureSummary(row.Error, l)
    }
    if err := c.uxShowDocument(ctx, uxBCheckDocument(row, l, false)); err != nil {
        return err
    }
    for {
        choices := []interaction.Choice{
            {Label: l.Text("ux_b_overview"), Value: "overview"},
            {Label: l.Text("ux_b_technical"), Value: "technical"},
            {Label: l.Text("ux_core_back"), Value: "back"},
        }
        action, err := c.input.SelectWithSummary(ctx, row.Target, summary, choices, "back")
        if err != nil {
            return err
        }
        if action == "back" {
            _, err := fmt.Fprintln(c.output.err, row.Target+" — "+summary)
            return err
        }
        doc := uxBCheckDocument(row, l, action == "technical")
        if err := c.uxShowDocument(ctx, doc); err != nil {
            return err
        }
    }
}

func uxBCheckState(row checkResult, l *i18n.Localizer) string {
    if row.Error != nil {
        return l.Text("check_step_state_failed")
    }
    if row.Steps["connect"] != "verified" {
        return l.Text("check_step_state_not_checked")
    }
    return l.Text("check_step_state_verified")
}

func uxBCoverage(batch checkBatch, l *i18n.Localizer) string {
    return l.Text("ux_b_coverage", map[string]any{"Count": batch.Completed + batch.Failed, "Total": batch.Selected, "Value": batch.Failed, "Matched": batch.Unexamined})
}

func uxBPrintCheck(out io.Writer, row checkResult, l *i18n.Localizer) {
    fmt.Fprintln(out, uxBCheckDocument(row, l, false).Content(76, false, false))
}

func uxBPrintCheckBatch(out io.Writer, batch checkBatch, l *i18n.Localizer) {
    for _, row := range batch.Checks {
        uxBPrintCheck(out, row, l)
    }
    fmt.Fprintln(out, uxBCoverage(batch, l))
}

// uxBCheckDocument keeps raw diagnostics out of the UI and puts long paths and
// version output on a separate, paginated technical page. JSON is unchanged.
func uxBCheckDocument(row checkResult, l *i18n.Localizer, technical bool) interaction.Document {
    doc := interaction.Document{Title: row.Target + " — " + uxBCheckState(row, l)}
    var body strings.Builder
    if technical {
        doc.Title = row.Target + " · " + l.Text("ux_b_technical")
        fmt.Fprintf(&body, "%s: %s\n", l.Text("remote_directory"), uxBKnown(row.Report.RemoteDir, l))
        for _, group := range []struct {
            label string
            tools map[string]ssh.Tool
        }{{"local_tools", row.Report.Local}, {"remote_tools", row.Report.Remote}} {
            fmt.Fprintln(&body, l.Text(group.label))
            for _, name := range []string{"ssh", "rsync", "python", "uv", "nvidia-smi", "nvcc"} {
                tool, exists := group.tools[name]
                if !exists {
                    continue
                }
                toolPath := tool.Path
                if toolPath == "" {
                    toolPath = l.Text("missing")
                }
                fmt.Fprintf(&body, "  %s: %s\n", name, toolPath)
                if tool.Version != "" {
                    fmt.Fprintln(&body, tool.Version)
                }
            }
        }
        if row.Runner != nil {
            fmt.Fprintln(&body, l.Text("runner_verified", map[string]any{"Version": row.Runner.Version, "Path": row.Runner.Path}))
        }
    } else {
        for _, step := range []string{"connect", "authentication", "platform", "runner"} {
            state := row.Steps[step]
            if state == "" {
                state = "not_checked"
            }
            doc.Rows = append(doc.Rows, interaction.Row{Label: l.Text("check_step_" + step), Value: l.Text("check_step_state_" + state)})
        }
        if row.Report.OS != "" {
            fmt.Fprintf(&body, "%s: %s / %s\n", l.Text("platform"), row.Report.OS, uxBKnown(row.Report.Arch, l))
        }
        for _, group := range []struct {
            label string
            tools map[string]ssh.Tool
        }{{"local_tools", row.Report.Local}, {"remote_tools", row.Report.Remote}} {
            tools := []string{}
            for _, name := range []string{"ssh", "rsync", "python", "uv", "nvidia-smi", "nvcc"} {
                tool, exists := group.tools[name]
                if exists {
                    status := l.Text("ux_b_found")
                    if tool.Path == "" {
                        status = l.Text("missing")
                    }
                    tools = append(tools, name+" ("+status+")")
                }
            }
            if len(tools) > 0 {
                fmt.Fprintf(&body, "%s: %s\n", l.Text(group.label), strings.Join(tools, ", "))
            }
        }
        resources := row.Report.Resources
        if resources == nil {
            resources = &ssh.ResourceObservation{}
        }
        printResourceObservation(&body, resources, l)
        if len(resources.GPUs) == 0 {
            fmt.Fprintf(&body, "%s: %s\n", l.Text("gpu"), l.Text("probe_unknown"))
        }
        if row.Report.OS != "" {
            fmt.Fprintf(&body, "%s: %.2f GiB\n", l.Text("disk"), float64(row.Report.DiskAvailableBytes)/(1<<30))
        }
        for _, issue := range row.Report.Issues {
            fmt.Fprintln(&body, "• "+l.Text(issue))
        }
        if row.Error != nil {
            doc.Context = row.Error.Code + ": " + uxBFailureSummary(row.Error, l)
            fmt.Fprintln(&body, uxBRecovery(row.Error, l))
        }
    }
    doc.Body = uxBDisplay(strings.TrimSpace(body.String()))
    return doc
}

func uxBDisplay(text string) string {
    return strings.Map(func(r rune) rune {
        if unicode.IsControl(r) && r != '\n' && r != '\t' {
            return '�'
        }
        return r
    }, ansi.Strip(text))
}

func uxBKnown(value string, l *i18n.Localizer) string {
    if value == "" {
        return l.Text("probe_unknown")
    }
    return value
}

func uxBFailureSummary(err *errorView, l *i18n.Localizer) string {
    switch err.FailureReason {
    case "host_key_unknown", "host_key_changed":
        return l.Text("ux_b_" + err.FailureReason)
    }
    kind := err.FailureKind
    if kind == "" {
        kind = "operation_failed"
    }
    return l.Text("trace_reason_" + kind)
}

func uxBRecovery(err *errorView, l *i18n.Localizer) string {
    reason := err.FailureReason
    if reason != "host_key_unknown" && reason != "host_key_changed" {
        reason = err.FailureKind
    }
    switch reason {
    case "host_key_unknown", "host_key_changed", "host_trust", "authentication", "configuration", "name_resolution", "connection_refused", "remote_prerequisite", "runner":
    case "connection_timeout", "observation_timeout":
        reason = "timeout"
    case "connection_closed", "connection_reset", "private_master_exit", "cloud_transport", "ssh_transport":
        reason = "disconnected"
    case "canceled":
        reason = "canceled"
    default:
        reason = "other"
    }
    hint := l.Text("ux_b_recover_" + reason)
    if err.Code == "EXEC_UNCONFIRMED" {
        hint += "\n" + l.Text("exec_unconfirmed_hint")
    }
    return hint
}
