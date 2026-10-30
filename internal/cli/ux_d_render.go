package cli

import (
    "fmt"
    "strings"
    "time"
    "unicode"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    execution "github.com/elebirds/loaf/internal/run"
)

func uxDSafe(text string) string {
    return strings.Map(func(r rune) rune {
        if unicode.IsControl(r) {
            return ' '
        }
        return r
    }, text)
}

func uxDCell(text string, width int) string {
    text = ansi.Truncate(uxDSafe(text), width, "…")
    return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func (c *invocation) uxDColumns() string {
    l := c.output.locale
    return c.uxDTableRow(l.Text("ux_d_column_name"), l.Text("ux_d_column_target"), l.Text("ux_d_column_state"), l.Text("ux_d_column_time"), "ID")
}

func (c *invocation) uxDTableRow(name, target, status, date, id string) string {
    width := max(50, min(96, interaction.Width(c.options.Err))-14)
    return uxDCell(name, width-42) + " " + uxDCell(target, 8) + " " + uxDCell(status, 13) + " " + uxDCell(date, 11) + " " + uxDCell(id, 6)
}

func (c *invocation) uxDRunRow(v execution.View) string {
    name := v.Name
    if name == "" {
        name = "—"
    }
    if len(v.Tags) > 0 {
        name += " #" + strings.Join(v.Tags, " #")
    }
    status, phase := localizedRunState(v.Status, c.output.locale), v.Phase
    if v.ObservedAt.IsZero() && v.Status == "unknown" && v.LastObservation != nil {
        status, phase = "~"+localizedRunState(v.LastObservation.Status, c.output.locale), v.LastObservation.Phase
    }
    if phase != "" {
        status += "/" + localizedRunPhase(phase, c.output.locale)
    }
    if v.QueryError != "" {
        status = "Q! " + status
    } else if v.LastQueryFailure != nil && v.ObservedAt.IsZero() {
        status = "Q~ " + status
    }
    if v.StateError != "" {
        status = "S! " + status
    }
    date := v.CreatedAt
    if v.Ref.AttemptedAt != nil {
        date = *v.Ref.AttemptedAt
    }
    stamp := "—"
    if !date.IsZero() {
        stamp = date.UTC().Format("01-02 15:04")
    }
    id := v.ID
    if len(id) > 6 {
        id = id[len(id)-6:]
    }
    return c.uxDTableRow(name, v.Target, status, stamp, id)
}

func (c *invocation) uxDSubmission(value string) string {
    switch value {
    case "submitted", "uncertain", "preparing":
        return c.output.locale.Text("ux_d_submission_" + value)
    }
    return value
}

func (c *invocation) uxDTaskSummary(s uxDTaskState) string {
    l, v := c.output.locale, s.view()
    width := max(24, min(96, interaction.Width(c.options.Err))-16)
    lines := []string{
        ansi.Truncate(uxDSafe(s.saved.ID+" · "+s.saved.Target), width, "…"),
        l.Text("ux_d_submission") + ": " + c.uxDSubmission(v.Submission),
        l.Text("ux_d_execution") + ": " + localizedRunState(v.Status, l),
    }
    if !v.ObservedAt.IsZero() {
        lines[2] += " · " + v.ObservedAt.UTC().Format(time.RFC3339)
    } else if s.verified == nil {
        lines[2] += " · " + l.Text("ux_d_saved")
    }
    if s.latest != nil && (s.latest.QueryError != "" || s.stateFailure != "") {
        lines = append(lines, l.Text("ux_d_failures", map[string]any{"Query": uxDValue(s.latest.QueryError), "State": uxDValue(s.stateFailure)}))
    } else if s.cause != nil {
        e := c.output.error(s.cause)
        lines = append(lines, e.Code+": "+e.Message)
    } else if s.saved.LastObservation != nil && s.verified == nil {
        old := s.saved.LastObservation
        lines = append(lines, "~ "+localizedRunState(old.Status, l)+" · "+old.ObservedAt.UTC().Format(time.RFC3339))
    } else if !s.saved.Compatible {
        lines = append(lines, l.Text("inspect_use_version", map[string]any{"Version": s.saved.Version}))
    }
    return strings.Join(lines, "\n")
}

func uxDValue(value string) string {
    if value == "" {
        return "—"
    }
    return value
}

func (c *invocation) uxDTaskDocument(s uxDTaskState) interaction.Document {
    l, saved, v := c.output.locale, s.saved, s.view()
    rows := []interaction.Row{}
    add := func(id, value string) {
        rows = append(rows, interaction.Row{Label: l.Text(id), Value: uxDSafe(uxDValue(value))})
    }
    add("ux_d_id", saved.ID)
    add("ux_d_column_name", saved.Name)
    add("ux_d_filter_tags", strings.Join(saved.Tags, " · "))
    add("ux_d_filter_target", saved.Target)
    add("ux_d_submission", c.uxDSubmission(v.Submission))
    add("ux_d_execution", localizedRunState(v.Status, l))
    add("ux_d_phase", localizedRunPhase(v.Phase, l))
    if !v.ObservedAt.IsZero() {
        add("ux_d_observed_at", v.ObservedAt.Format(time.RFC3339Nano))
    }
    if v.ExitCode != nil {
        add("ux_d_exit", fmt.Sprint(*v.ExitCode))
    }
    if v.Signal != "" {
        add("ux_d_signal", v.Signal)
    }
    if v.CompletedAt != nil {
        add("ux_d_completed", v.CompletedAt.Format(time.RFC3339Nano))
    }
    if saved.AttemptedAt != nil {
        add("ux_d_attempted", saved.AttemptedAt.Format(time.RFC3339Nano))
    }
    if old := saved.LastObservation; old != nil {
        value := localizedRunState(old.Status, l) + " / " + localizedRunPhase(old.Phase, l) + " · " + old.ObservedAt.Format(time.RFC3339Nano)
        if old.ExitCode != nil {
            value += " · " + l.Text("run_exit", map[string]any{"Value": *old.ExitCode})
        }
        add("ux_d_last_saved", value)
    }
    if failure := saved.LastQueryFailure; failure != nil {
        add("ux_d_last_failure", failure.Code+" / "+failure.Stage+" · "+failure.ObservedAt.Format(time.RFC3339Nano))
    }
    if s.latest != nil {
        add("ux_d_query_error", s.latest.QueryError)
        add("ux_d_state_error", s.stateFailure)
    }
    if saved.CleanedAt != nil {
        add("clean_status_cleaned", saved.CleanedAt.Format(time.RFC3339Nano))
    } else if saved.CleanupPending {
        add("clean_status_pending", saved.ID)
    }
    if saved.Instance != nil {
        add("ux_d_instance", saved.Instance.Provider+" / "+saved.Instance.Product+" / "+saved.Instance.Account+" / "+saved.Instance.ID)
    } else {
        add("ux_d_destination", saved.Destination)
    }
    add("ux_d_version", saved.Version)
    add("ux_d_client_version", saved.ClientVersion)
    add("ux_core_project", saved.ProjectRoot)
    add("ux_d_remote", saved.RemoteDir)
    add("ux_d_outputs", strings.Join(saved.Outputs, " · "))
    doc := interaction.Document{Title: l.Text("ux_d_details"), Rows: rows}
    if s.verified == nil {
        doc.Body = l.Text("inspect_local")
    }
    if s.cause != nil {
        e := c.output.error(s.cause)
        doc.Body += "\n" + e.Code + ": " + e.Message
    }
    return doc
}
