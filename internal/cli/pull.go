package cli

import (
    "context"
    "fmt"
    "io"
    "strconv"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/spf13/cobra"
)

// OutputPuller retrieves saved-run outputs without owning CLI rendering.
type OutputPuller interface {
    Pull(context.Context, config.RunRef, string, []string, config.PullPolicy, bool) (sources.PullResult, error)
}

func newPullCommand(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any)) *cobra.Command {
    var paths []string
    var to string
    var checksum bool
    var into string
    var concurrency int
    cmd := &cobra.Command{Use: "pull " + l.Text("run_id_arg"), Short: l.Text("pull_short"), Args: arguments(0, 1000)}
    cmd.Flags().StringArrayVar(&paths, "path", nil, l.Text("flag_pull_path"))
    cmd.Flags().StringVar(&to, "to", "", l.Text("flag_pull_to"))
    cmd.Flags().BoolVar(&checksum, "checksum", false, l.Text("flag_pull_checksum"))
    cmd.Flags().StringVar(&into, "into", "", l.Text("flag_pull_into"))
    cmd.Flags().IntVar(&concurrency, "concurrency", 1, l.Text("flag_pull_concurrency"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        multi := len(args) > 1 || cmd.Flags().Changed("into")
        if (multi && (into == "" || len(args) == 0 || cmd.Flags().Changed("to"))) || (!multi && cmd.Flags().Changed("concurrency")) || concurrency < 1 || concurrency > 4 || (concurrency > 1 && o.Interactive) {
            return fault.New("BAD_CONFIG", "pull_batch_flags", nil)
        }
        project, file, err := runProject(cmd, o, target)
        if err != nil {
            return err
        }
        layers := []config.PullSettings{cfg.Pull, file.Pull}
        if cmd.Flags().Changed("checksum") {
            layers = append(layers, config.PullSettings{Checksum: &checksum})
        }
        policy, err := config.ResolvePull(layers...)
        if err != nil {
            return err
        }
        if cmd.Flags().Changed("to") && to == "" {
            return fault.New("BAD_CONFIG", "pull_destination", nil)
        }
        if multi {
            if o.Puller == nil {
                return fault.New("SSH_FAILED", "ssh_missing", nil)
            }
            refs, err := o.Runs.Freeze(project.ID, args, nil)
            if err != nil {
                return err
            }
            jsonMode, _ := cmd.Flags().GetBool("json")
            pulled, err := batchPull(cmd.Context(), o, project, refs, into, paths, cmd.Flags().Changed("path"), policy, concurrency, jsonMode || !o.Interactive)
            result(pulled)
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
        selected := ref.Outputs
        if cmd.Flags().Changed("path") {
            selected = paths
        }
        selected, err = sources.OutputPaths(selected)
        if err != nil {
            return err
        }
        destination, err := sources.Destination(project.Root, o.CWD, to, ref.ID, policy)
        if err != nil {
            return err
        }
        if o.Puller == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        if !jsonMode {
            fmt.Fprintln(o.Err, l.Text("pull_progress", map[string]any{"ID": ref.ID, "Target": ref.Target}))
        }
        pulled, err := o.Puller.Pull(cmd.Context(), ref, destination, selected, policy, jsonMode || !o.Interactive)
        result(pulled)
        return err
    }
    return cmd
}

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
