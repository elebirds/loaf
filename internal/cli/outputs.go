package cli

import (
    "fmt"
    "io"
    "strconv"

    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
)

func printOutputs(out io.Writer, r execution.OutputListing, l *i18n.Localizer) {
    fmt.Fprintln(out, l.Text("outputs_heading", map[string]any{"ID": r.RunID}))
    var observedBytes int64
    observedFiles := 0
    for _, entry := range r.Entries {
        if entry.Type == "file" {
            observedBytes += entry.Size
            observedFiles++
        }
        kind := entry.Type
        switch kind {
        case "file", "directory", "symlink":
            kind = l.Text("output_type_" + kind)
        }
        fmt.Fprintf(out, "%s  %d  %s  %s\n", kind, entry.Size, entry.ModifiedAt.Format("2006-01-02T15:04:05Z07:00"), strconv.Quote(entry.Path))
    }
    for _, name := range r.Missing {
        fmt.Fprintln(out, l.Text("output_missing_entry", map[string]any{"Path": strconv.Quote(name)}))
    }
    fmt.Fprintln(out, l.Text("outputs_coverage", map[string]any{"Count": r.Examined, "Matched": r.RootsExamined, "Total": r.RootsTotal, "Value": r.DepthLimited}))
    fmt.Fprintln(out, l.Text("outputs_observed_bytes", map[string]any{"Count": observedFiles, "Bytes": observedBytes}))
    if r.Truncated {
        fmt.Fprintln(out, l.Text("outputs_truncated"))
    }
}
