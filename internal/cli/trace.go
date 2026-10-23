package cli

import (
    "fmt"
    "io"

    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
)

func printTrace(out io.Writer, trace ssh.TraceReport, l *i18n.Localizer) {
    fmt.Fprintln(out, l.Text("trace_summary", map[string]any{"Millis": trace.TotalMillis, "Masters": trace.MastersEstablished, "Reconnects": trace.Reconnects, "Retries": trace.ReadRetries, "Resolutions": trace.ConnectionResolutions}))
    for _, stage := range trace.Stages {
        reason := ""
        if stage.Reason != "" {
            reason = l.Text("trace_reason_" + stage.Reason)
        }
        fmt.Fprintln(out, l.Text("trace_stage", map[string]any{"Stage": l.Text("operation_stage_" + stage.Stage), "Millis": stage.ElapsedMillis, "Outcome": l.Text("trace_outcome_" + stage.Outcome), "Code": stage.ErrorCode, "Reason": reason}))
    }
    if trace.Truncated {
        fmt.Fprintln(out, l.Text("trace_truncated"))
    }
}
