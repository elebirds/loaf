package cli

import (
    "fmt"
    "io"
    "path"
    "strconv"

    "github.com/elebirds/loaf/internal/i18n"
)

func printSync(out io.Writer, r syncResult, l *i18n.Localizer) {
    if r.DryRun {
        fmt.Fprintln(out, l.Text("sync_preview"))
    } else {
        fmt.Fprintln(out, l.Text("sync_done"))
    }
    fmt.Fprintln(out, l.Text("sync_selection", map[string]any{"Count": r.SelectedFiles, "Bytes": r.SelectedBytes}))
    if r.SelectedBytes >= 1<<30 {
        fmt.Fprintln(out, l.Text("source_large_warning"))
    }
    fmt.Fprintln(out, l.Text("sync_policy", map[string]any{"Mode": l.Text("sync_mode_" + r.Policy.Mode)}))
    ignore := "sync_ignore_on"
    if !r.Policy.RespectGitignore {
        ignore = "sync_ignore_off"
    }
    fmt.Fprintln(out, l.Text(ignore))
    if r.Policy.Mode == "update" {
        fmt.Fprintln(out, l.Text("sync_update_warning"))
    }
    if r.ChangedFiles != nil {
        fmt.Fprintln(out, l.Text("sync_changes", map[string]any{"Count": *r.ChangedFiles}))
        fmt.Fprintln(out, l.Text("sync_cache", map[string]any{"Path": path.Join(r.RemoteDir, "cache")}))
    }
    if r.DryRun {
        for _, file := range r.Files {
            value := strconv.Quote(file.Path)
            if file.Kind == "symlink" {
                value += " -> " + strconv.Quote(file.Link)
            }
            fmt.Fprintln(out, "  + "+value)
        }
        if len(r.Excluded) > 0 {
            fmt.Fprintln(out, l.Text("sync_exclusions"))
        }
    }
    for _, ex := range r.Excluded {
        if !r.DryRun && ex.Reason != "unsafe_link" && ex.Reason != "broken_link" && ex.Reason != "excluded_link_target" && ex.Reason != "special_file" {
            continue
        }
        reason := l.Text("sync_reason_" + ex.Reason)
        if ex.Rule != "" {
            reason += " (" + strconv.Quote(ex.Rule) + ")"
        }
        fmt.Fprintf(out, "  - %s: %s\n", strconv.Quote(ex.Path), reason)
    }
}
