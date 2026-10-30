package cli

import (
    "fmt"
    "io"
    "time"

    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
)

func printRun(out io.Writer, v execution.View, l *i18n.Localizer) {
    state := v.Status
    switch state {
    case "starting", "running", "succeeded", "failed", "stopped", "unknown":
        state = l.Text("run_status_" + state)
    }
    fmt.Fprintf(out, "%s  %s  %s", v.ID, v.Target, state)
    if v.Name != "" {
        fmt.Fprintf(out, "  %q", v.Name)
    }
    if len(v.Tags) > 0 {
        fmt.Fprintf(out, "  %v", v.Tags)
    }
    if v.Status == "unknown" && v.Submission == "submitted" {
        fmt.Fprintf(out, "  %s", l.Text("run_submission_confirmed"))
    }
    if v.Submission == "uncertain" {
        fmt.Fprintf(out, "  %s", l.Text("run_submission_uncertain"))
    }
    if v.Submission == "preparing" {
        fmt.Fprintf(out, "  %s", l.Text("run_submission_preparing"))
    }
    if v.Phase != "" {
        fmt.Fprintf(out, "  %s", l.Text("run_phase_"+v.Phase))
    }
    if v.ExitCode != nil {
        fmt.Fprintf(out, "  %s", l.Text("run_exit", map[string]any{"Value": *v.ExitCode}))
    }
    if v.Signal != "" {
        fmt.Fprintf(out, "  %s", v.Signal)
    }
    if v.Reason != "" {
        fmt.Fprintf(out, "  %s", l.Text("run_reason_"+v.Reason))
    }
    if v.CleanedAt != nil {
        fmt.Fprintf(out, "  %s", l.Text("clean_status_cleaned"))
    } else if v.CleanupPending {
        fmt.Fprintf(out, "  %s", l.Text("clean_status_pending"))
    }
    if v.Abandonment != nil {
        state := "abandon_status_pending"
        if v.Abandonment.NoStartAt != nil {
            state = "abandon_status_fenced"
        }
        if v.Abandonment.CleanedAt != nil {
            state = "abandon_status_cleaned"
        }
        fmt.Fprintf(out, "  %s", l.Text(state))
    }
    if v.QueryInterrupted {
        fmt.Fprintf(out, "  %s", l.Text("wait_interrupted_row"))
    }
    if v.QueryError != "" {
        fmt.Fprintf(out, "  %s: %s", l.Text("run_query"), v.QueryError)
    }
    if v.StateError != "" {
        fmt.Fprintf(out, "  %s", l.Text("run_state_save_failed", map[string]any{"Code": v.StateError}))
    }
    fmt.Fprintln(out)
    if v.LastObservation != nil {
        fmt.Fprintln(out, l.Text("last_observation", map[string]any{"Value": localizedRunState(v.LastObservation.Status, l), "Code": localizedRunPhase(v.LastObservation.Phase, l), "Stage": v.LastObservation.ObservedAt.Format(time.RFC3339)}))
        if v.LastObservation.ExitCode != nil {
            fmt.Fprintln(out, l.Text("last_observed_exit", map[string]any{"Value": *v.LastObservation.ExitCode}))
        }
    }
    if v.LastQueryFailure != nil {
        fmt.Fprintln(out, l.Text("last_query_failure", map[string]any{"Code": v.LastQueryFailure.Code, "Stage": v.LastQueryFailure.Stage, "Value": v.LastQueryFailure.ObservedAt.Format(time.RFC3339)}))
    }
    if v.Log != nil {
        fmt.Fprint(out, v.Log.Text)
        if v.Log.Truncated {
            fmt.Fprintln(out, l.Text("logs_truncated"))
        }
    }
}

func printRunNext(out io.Writer, v execution.View, l *i18n.Localizer, waited bool) {
    if v.Abandonment != nil {
        return
    }
    if v.Submission == "preparing" {
        fmt.Fprintln(out, l.Text("run_next_inspect", map[string]any{"ID": v.ID}))
        return
    }
    if v.Submission == "uncertain" || v.QueryError != "" || v.Status == "unknown" {
        fmt.Fprintln(out, l.Text("run_next_query", map[string]any{"ID": v.ID}))
        return
    }
    if v.Status == "succeeded" {
        fmt.Fprintln(out, l.Text("run_next_pull", map[string]any{"ID": v.ID}))
        return
    }
    if v.Status == "failed" || v.Status == "stopped" {
        fmt.Fprintln(out, l.Text("run_next_logs", map[string]any{"ID": v.ID}))
        return
    }
    if waited {
        fmt.Fprintln(out, l.Text("run_wait_finished"))
    }
    fmt.Fprintln(out, l.Text("run_next_running", map[string]any{"ID": v.ID}))
}

// printRunRow keeps navigation compact without hiding errors or dated facts.
func printRunRow(out io.Writer, v execution.View, l *i18n.Localizer) {
    fmt.Fprintf(out, "%s  %s  %s", v.ID, v.Target, localizedRunState(v.Status, l))
    if v.Name != "" {
        fmt.Fprintf(out, "  %q", v.Name)
    }
    fmt.Fprintln(out)
    if !v.CreatedAt.IsZero() {
        fmt.Fprintln(out, "  "+v.CreatedAt.Format(time.RFC3339))
    }
    if v.Phase != "" {
        fmt.Fprintln(out, "  "+localizedRunPhase(v.Phase, l))
    }
    if v.Submission == "uncertain" {
        fmt.Fprintln(out, "  "+l.Text("run_submission_uncertain"))
    }
    if v.QueryError != "" {
        fmt.Fprintln(out, "  "+l.Text("run_query")+": "+v.QueryError)
    }
    if v.StateError != "" {
        fmt.Fprintln(out, "  "+l.Text("run_state_save_failed", map[string]any{"Code": v.StateError}))
    }
    if v.LastObservation != nil {
        fmt.Fprintln(out, l.Text("last_observation", map[string]any{"Value": localizedRunState(v.LastObservation.Status, l), "Code": localizedRunPhase(v.LastObservation.Phase, l), "Stage": v.LastObservation.ObservedAt.Format(time.RFC3339)}))
    }
    if v.LastQueryFailure != nil {
        fmt.Fprintln(out, l.Text("last_query_failure", map[string]any{"Code": v.LastQueryFailure.Code, "Stage": v.LastQueryFailure.Stage, "Value": v.LastQueryFailure.ObservedAt.Format(time.RFC3339)}))
    }
    if v.CleanedAt != nil {
        fmt.Fprintln(out, "  "+l.Text("clean_status_cleaned"))
    } else if v.CleanupPending {
        fmt.Fprintln(out, "  "+l.Text("clean_status_pending"))
    }
}

func localizedRunState(state string, l *i18n.Localizer) string {
    switch state {
    case "starting", "running", "succeeded", "failed", "stopped", "unknown":
        return l.Text("run_status_" + state)
    }
    return state
}
func localizedRunPhase(phase string, l *i18n.Localizer) string {
    if phase == "" {
        return ""
    }
    return l.Text("run_phase_" + phase)
}
