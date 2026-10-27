package cli

import (
    "fmt"
    "io"

    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
)

func printLocalDetails(out io.Writer, v execution.LocalDetails, l *i18n.Localizer) {
    printRun(out, v.View, l)
    fmt.Fprintln(out, l.Text("inspect_local"))
    fmt.Fprintln(out, l.Text("inspect_version", map[string]any{"Version": v.Version, "Client": v.ClientVersion}))
    if v.Instance != nil {
        fmt.Fprintln(out, l.Text("inspect_instance", map[string]any{"Provider": v.Instance.Provider, "Product": v.Instance.Product, "Account": v.Instance.Account, "ID": v.Instance.ID}))
    } else {
        fmt.Fprintln(out, l.Text("inspect_destination", map[string]any{"Destination": v.Destination}))
    }
    fmt.Fprintln(out, l.Text("inspect_location", map[string]any{"Root": v.ProjectRoot, "Remote": v.RemoteDir}))
    fmt.Fprintln(out, l.Text("inspect_outputs", map[string]any{"Paths": v.Outputs}))
    if !v.Compatible {
        fmt.Fprintln(out, l.Text("inspect_use_version", map[string]any{"Version": v.Version}))
    }
}
