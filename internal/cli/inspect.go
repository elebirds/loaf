package cli

import (
    "fmt"
    "io"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/spf13/cobra"
)

func newInspectCommand(o Options, l *i18n.Localizer, result func(any)) *cobra.Command {
    cmd := &cobra.Command{Use: "inspect " + l.Text("inspect_args"), Short: l.Text("inspect_short"), Args: arguments(1, 1)}
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        if cmd.Flags().Changed("target") {
            return fault.New("BAD_CONFIG", "run_bound_target", nil)
        }
        root, _, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        project, err := o.Store.Existing(root)
        if err != nil {
            return err
        }
        service := execution.Service{Index: o.Store, Metadata: o.Store, Observations: o.Store}
        details, err := service.Inspect(project, args[0])
        if err == nil {
            result(details)
        }
        return err
    }
    return cmd
}

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
