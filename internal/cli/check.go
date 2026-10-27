package cli

import (
    "fmt"
    "io"

    "github.com/elebirds/loaf/internal/i18n"
)

func printCheckBatch(out io.Writer, r checkBatch, l *i18n.Localizer) {
    for _, row := range r.Checks {
        fmt.Fprintln(out, l.Text("check_heading", map[string]any{"Target": row.Target}))
        for _, step := range []string{"connect", "authentication", "platform", "runner"} {
            fmt.Fprintf(out, "  %s: %s\n", l.Text("check_step_"+step), l.Text("check_step_state_"+row.Steps[step]))
        }
        if row.Error != nil {
            fmt.Fprintln(out, row.Error.Code+"  "+l.Text("trace_reason_"+row.Error.FailureKind))
        }
    }
    fmt.Fprintln(out, l.Text("check_batch_summary", map[string]any{"Count": r.Completed, "Total": r.Selected, "Value": r.Failed, "Matched": r.Unexamined}))
}
