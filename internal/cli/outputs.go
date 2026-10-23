package cli

import (
    "context"
    "fmt"
    "io"
    "strconv"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/spf13/cobra"
)

// OutputBrowser observes bounded output metadata for a saved run.
type OutputBrowser interface {
    List(context.Context, config.RunRef, int, int, bool, bool) (execution.OutputListing, error)
}

func newOutputsCommand(o Options, target *string, l *i18n.Localizer, result func(any)) *cobra.Command {
    var depth, limit int
    var prepare bool
    cmd := &cobra.Command{Use: "outputs " + l.Text("run_id_arg"), Short: l.Text("outputs_short"), Args: arguments(0, 1)}
    cmd.Flags().IntVar(&depth, "depth", 2, l.Text("flag_outputs_depth"))
    cmd.Flags().IntVar(&limit, "limit", 200, l.Text("flag_outputs_limit"))
    cmd.Flags().BoolVar(&prepare, "prepare-runner", false, l.Text("flag_outputs_prepare"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        if depth < 0 || depth > 20 || limit < 1 || limit > 1000 {
            return fault.New("BAD_CONFIG", "outputs_bounds", nil)
        }
        project, _, err := runProject(cmd, o, target)
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
        if o.Outputs == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        listing, err := o.Outputs.List(cmd.Context(), ref, depth, limit, prepare, jsonMode || !o.Interactive)
        result(listing)
        return err
    }
    return cmd
}
func printOutputs(out io.Writer, r execution.OutputListing, l *i18n.Localizer) {
    fmt.Fprintln(out, l.Text("outputs_heading", map[string]any{"ID": r.RunID}))
    for _, entry := range r.Entries {
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
    if r.Truncated {
        fmt.Fprintln(out, l.Text("outputs_truncated"))
    }
}
