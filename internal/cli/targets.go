package cli

import (
    "fmt"
    "io"

    "github.com/elebirds/loaf/internal/i18n"
)

type machineDetails struct{ targetsResult }

func printTargets(out io.Writer, data targetsResult, l *i18n.Localizer, details bool) {
    if data.Project.Target == "" {
        fmt.Fprintln(out, l.Text("targets_no_choice"))
    } else if !data.Project.Available {
        fmt.Fprintln(out, l.Text("targets_missing_choice", map[string]any{"Target": data.Project.Target}))
    } else {
        fmt.Fprintln(out, l.Text("targets_choice", map[string]any{"Target": data.Project.Target}))
    }
    if len(data.Targets) == 0 {
        fmt.Fprintln(out, l.Text("targets_empty"))
    }
    for _, entry := range data.Targets {
        marker := " "
        if entry.Selected {
            marker = "*"
        }
        destination := entry.Destination
        if entry.Cloud != nil {
            destination = fmt.Sprintf("%s/%s · %s", entry.Cloud.Ref.Account, entry.Cloud.Ref.Product, entry.Cloud.Ref.ID)
        }
        fmt.Fprintf(out, "%s %s  %s\n", marker, entry.Name, destination)
        if !details {
            continue
        }
        fmt.Fprintf(out, "  %s: %s\n", l.Text("targets_remote_base"), entry.RemoteBase)
        if len(entry.Args) > 0 {
            fmt.Fprintf(out, "  %s: %q\n", l.Text("targets_ssh_args"), entry.Args)
        }
    }
}
