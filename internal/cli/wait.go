package cli

import (
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/spf13/cobra"
)

func newWaitCommand(o Options, l *i18n.Localizer, result func(any)) *cobra.Command {
    var tags []string
    var timeout time.Duration
    cmd := &cobra.Command{Use: "wait " + l.Text("wait_args"), Short: l.Text("wait_short"), Args: arguments(0, 1000)}
    cmd.Flags().StringArrayVar(&tags, "tag", nil, l.Text("flag_run_tag"))
    cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, l.Text("flag_wait_timeout"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        if cmd.Flags().Changed("target") {
            return fault.New("BAD_CONFIG", "run_bound_target", nil)
        }
        if timeout <= 0 || timeout > 24*time.Hour {
            return fault.New("BAD_CONFIG", "wait_timeout", nil)
        }
        if o.Runs == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        root, _, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        project, err := o.Store.Existing(root)
        if err != nil {
            return err
        }
        refs, err := o.Runs.Freeze(project.ID, args, tags)
        if err != nil {
            return err
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        var changed func(execution.View)
        if !jsonMode {
            changed = func(v execution.View) { printRun(o.Err, v, l) }
        }
        observed, err := o.Runs.Wait(cmd.Context(), refs, timeout, jsonMode || !o.Interactive, changed)
        result(observed)
        return err
    }
    return cmd
}
