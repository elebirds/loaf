package cli

import (
    "fmt"
    "io"

    "github.com/elebirds/loaf/internal/i18n"
)

func printClean(out io.Writer, batch cleanBatch, l *i18n.Localizer) {
    for _, item := range batch.Runs {
        state := "clean_preview"
        if item.Cleaned {
            state = "clean_done"
        } else if item.ErrorCode != "" {
            state = "clean_incomplete"
        }
        fmt.Fprintln(out, l.Text(state, map[string]any{"ID": item.Run.ID, "Target": item.Run.Target, "Path": item.Path}))
        if item.ErrorCode != "" {
            fmt.Fprintln(out, "  "+item.ErrorCode)
        }
    }
    if batch.DryRun {
        fmt.Fprintln(out, l.Text("clean_preview_note"))
    }
    fmt.Fprintln(out, l.Text("clean_retained"))
}
