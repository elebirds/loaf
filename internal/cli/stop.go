package cli

import (
    "fmt"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/spf13/cobra"
)

func newStopCommand(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any)) *cobra.Command {
    var grace string
    cmd := &cobra.Command{Use: "stop " + l.Text("run_id_arg"), Short: l.Text("stop_short"), Args: arguments(0, 1)}
    cmd.Flags().StringVar(&grace, "grace", "10s", l.Text("flag_stop_grace"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        project, file, err := runProject(cmd, o, target)
        if err != nil {
            return err
        }
        layers := []config.StopSettings{cfg.Stop, file.Stop}
        if cmd.Flags().Changed("grace") {
            if grace == "" {
                return fault.New("BAD_CONFIG", "invalid_stop_grace", nil)
            }
            layers = append(layers, config.StopSettings{Grace: grace})
        }
        policy, err := config.ResolveStopGrace(layers...)
        if err != nil {
            return err
        }
        id := ""
        if len(args) > 0 {
            id = args[0]
        }
        ref, err := o.Runs.Select(project.ID, id)
        if err != nil {
            return err
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        if !jsonMode {
            fmt.Fprintln(o.Err, l.Text("stop_progress"))
        }
        stopped, err := o.Runs.Stop(cmd.Context(), ref, policy, jsonMode || !o.Interactive)
        result(stopped)
        return err
    }
    return cmd
}
