package cli

import (
    "context"
    "fmt"
    "io"
    "strings"
    "unicode"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
)

func (c *invocation) check(ctx context.Context) (any, error) {
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

func checkState(row checkResult, l *i18n.Localizer) string {
    if row.Error != nil {
        return l.Text("check_step_state_failed")
    }
    if row.Steps["connect"] != "verified" {
        return l.Text("check_step_state_not_checked")
    }
    return l.Text("check_step_state_verified")
}

func checkCoverage(batch checkBatch, l *i18n.Localizer) string {
    return l.Text("ux_b_coverage", map[string]any{"Count": batch.Completed + batch.Failed, "Total": batch.Selected, "Value": batch.Failed, "Matched": batch.Unexamined})
}

func printCheck(out io.Writer, row checkResult, l *i18n.Localizer) {
    fmt.Fprintln(out, checkDocument(row, l).Content(76, false, false))
}

func printCheckBatch(out io.Writer, batch checkBatch, l *i18n.Localizer) {
    for _, row := range batch.Checks {
        printCheck(out, row, l)
    }
    fmt.Fprintln(out, checkCoverage(batch, l))
}

// checkDocument summarizes finite observations; JSON retains technical details.

func checkDocument(row checkResult, l *i18n.Localizer) interaction.Document {
    doc := interaction.Document{Title: row.Target + " — " + checkState(row, l)}
    var body strings.Builder

    for _, step := range []string{"connect", "authentication", "platform", "runner"} {
        state := row.Steps[step]
        if state == "" {
            state = "not_checked"
        }
        doc.Rows = append(doc.Rows, interaction.Row{Label: l.Text("check_step_" + step), Value: l.Text("check_step_state_" + state)})
    }
    if row.Report.OS != "" {
        fmt.Fprintf(&body, "%s: %s / %s\n", l.Text("platform"), row.Report.OS, checkKnown(row.Report.Arch, l))
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
        doc.Context = row.Error.Code + ": " + checkFailureSummary(row.Error, l)
        fmt.Fprintln(&body, checkRecovery(row.Error, l))
    }
    doc.Body = checkDisplay(strings.TrimSpace(body.String()))
    return doc
}

func checkDisplay(text string) string {
    return strings.Map(func(r rune) rune {
        if unicode.IsControl(r) && r != '\n' && r != '\t' {
            return '�'
        }
        return r
    }, ansi.Strip(text))
}

func checkKnown(value string, l *i18n.Localizer) string {
    if value == "" {
        return l.Text("probe_unknown")
    }
    return value
}

func checkFailureSummary(err *errorView, l *i18n.Localizer) string {
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

func checkRecovery(err *errorView, l *i18n.Localizer) string {
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
