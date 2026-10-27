package cli

import (
    "fmt"
    "io"
    "strconv"

    "github.com/elebirds/loaf/internal/i18n"
    sources "github.com/elebirds/loaf/internal/sync"
)

func printPull(out io.Writer, r sources.PullResult, l *i18n.Localizer, ok bool) {
    message := "pull_done"
    if !ok {
        message = "pull_incomplete"
    }
    fmt.Fprintln(out, l.Text(message, map[string]any{"ID": r.RunID}))
    fmt.Fprintln(out, l.Text("pull_location", map[string]any{"Path": r.Destination}))
    if r.ChangedFiles != nil {
        fmt.Fprintln(out, l.Text("pull_changes", map[string]any{"Count": *r.ChangedFiles}))
    }
    for _, name := range r.DownloadedPaths {
        fmt.Fprintln(out, "  + "+strconv.Quote(name))
    }
    if len(r.MissingPaths) > 0 {
        fmt.Fprintln(out, l.Text("pull_missing_paths"))
        for _, name := range r.MissingPaths {
            fmt.Fprintln(out, "  - "+strconv.Quote(name))
        }
    }
}
