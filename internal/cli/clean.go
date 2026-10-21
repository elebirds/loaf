package cli

import (
    "bufio"
    "context"
    "errors"
    "fmt"
    "io"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/spf13/cobra"
)

// RunCleaner previews and deletes only already-selected explicit run IDs.
type RunCleaner interface {
    Preview(context.Context, config.RunRef, bool, bool) (execution.CleanResult, error)
    Clean(context.Context, execution.CleanResult, bool) (execution.CleanResult, error)
}

type cleanBatch struct {
    DryRun bool                    `json:"dry_run"`
    Runs   []execution.CleanResult `json:"runs"`
}

func newCleanCommand(o Options, target *string, l *i18n.Localizer, result func(any)) *cobra.Command {
    var dryRun, yes bool
    cmd := &cobra.Command{Use: "clean " + l.Text("clean_ids_arg"), Short: l.Text("clean_short"), Args: arguments(1, 1000)}
    cmd.Flags().BoolVar(&dryRun, "dry-run", false, l.Text("clean_flag_dry_run"))
    cmd.Flags().BoolVarP(&yes, "yes", "y", false, l.Text("clean_flag_yes"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        jsonMode, _ := cmd.Flags().GetBool("json")
        if !dryRun && !yes && (jsonMode || !o.Interactive) {
            return fault.New("BAD_CONFIG", "clean_confirmation_required", nil)
        }
        project, _, err := runProject(cmd, o, target)
        if err != nil {
            return err
        }
        if o.Cleaner == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        batch := cleanBatch{DryRun: dryRun, Runs: []execution.CleanResult{}}
        seen := map[string]bool{}
        var first error
        for _, id := range args {
            if seen[id] {
                continue
            }
            seen[id] = true
            ref, err := o.Runs.Select(project.ID, id)
            if err != nil {
                batch.Runs = append(batch.Runs, execution.CleanResult{Run: execution.View{ID: id}, ErrorCode: cleanErrorCode(err)})
                if first == nil {
                    first = err
                }
                continue
            }
            item, err := o.Cleaner.Preview(cmd.Context(), ref, jsonMode || !o.Interactive, !dryRun)
            if err != nil {
                item.ErrorCode = cleanErrorCode(err)
                if first == nil {
                    first = err
                }
            }
            batch.Runs = append(batch.Runs, item)
        }
        result(batch)
        // Validate every selection before mutating any of them. A preview is
        // not a stored permission: the remote helper rechecks before deletion.
        if first != nil || dryRun {
            return first
        }
        if !yes {
            printClean(o.Err, batch, l)
            fmt.Fprint(o.Err, l.Text("clean_confirm"))
            line, err := bufio.NewReader(o.In).ReadString('\n')
            if err != nil && err != io.EOF {
                return err
            }
            answer := strings.ToLower(strings.TrimSpace(line))
            if answer != "y" && answer != "yes" && answer != "是" {
                return fault.New("CLEAN_CANCELED", "clean_canceled", nil)
            }
        }
        for n, item := range batch.Runs {
            if cmd.Context().Err() != nil {
                item.ErrorCode = "CLEAN_NOT_ATTEMPTED"
                batch.Runs[n] = item
                first = fault.Wrap("CLEAN_UNCONFIRMED", "clean_unconfirmed", cmd.Context().Err())
                continue
            }
            cleaned, err := o.Cleaner.Clean(cmd.Context(), item, jsonMode || !o.Interactive)
            if err != nil {
                cleaned.ErrorCode = cleanErrorCode(err)
                if first == nil || cleanErrorCode(err) == "CLEAN_UNCONFIRMED" {
                    first = err
                }
            }
            batch.Runs[n] = cleaned
        }
        result(batch)
        return first
    }
    return cmd
}

func cleanErrorCode(err error) string {
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return "CLEAN_FAILED"
}

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
