package cli

import (
    "fmt"
    "io"
    "path"
    "path/filepath"
    "strconv"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/spf13/cobra"
)

type syncResult struct {
    DryRun          bool                `json:"dry_run"`
    Root            string              `json:"root"`
    Target          string              `json:"target,omitempty"`
    ProjectID       string              `json:"project_id,omitempty"`
    RemoteDir       string              `json:"remote_dir,omitempty"`
    Policy          config.SyncPolicy   `json:"policy"`
    SelectionSource string              `json:"selection_source"`
    SelectedFiles   int                 `json:"selected_files"`
    SelectedBytes   int64               `json:"selected_bytes"`
    ChangedFiles    *int                `json:"changed_files,omitempty"`
    Files           []sources.File      `json:"files,omitempty"`
    Excluded        []sources.Exclusion `json:"excluded"`
}

func newSyncCommand(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any)) *cobra.Command {
    var preview bool
    var mode string
    var respect bool
    var excluded []string
    cmd := &cobra.Command{Use: "sync --dry-run", Short: l.Text("sync_short"), Args: arguments(0, 0)}
    cmd.Flags().BoolVar(&preview, "dry-run", false, l.Text("flag_dry_run"))
    cmd.Flags().StringVar(&mode, "mode", "mirror", l.Text("flag_sync_mode"))
    cmd.Flags().BoolVar(&respect, "respect-gitignore", true, l.Text("flag_respect_gitignore"))
    cmd.Flags().StringArrayVar(&excluded, "exclude", nil, l.Text("flag_sync_exclude"))
    cmd.RunE = func(cmd *cobra.Command, _ []string) (err error) {
        if !preview {
            f := fault.New("BAD_CONFIG", "invalid_flags", nil)
            f.Hint = "help_hint"
            return f
        }
        defer func() {
            if err != nil && cmd.Context().Err() != nil {
                canceled := fault.Wrap("SYNC_CANCELED", "sync_canceled", cmd.Context().Err())
                if !preview {
                    canceled.Hint = "sync_retry"
                }
                err = canceled
            }
        }()
        root, file, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        explicit := config.SyncSettings{Exclude: excluded}
        if cmd.Flags().Changed("mode") {
            explicit.Mode = mode
        }
        if cmd.Flags().Changed("respect-gitignore") {
            explicit.RespectGitignore = &respect
        }
        policy, err := config.ResolveSync(cfg.Sync, file.Sync, explicit)
        if err != nil {
            return err
        }
        plan, err := sources.Select(cmd.Context(), root, sources.SelectOptions{Mode: policy.Mode, RespectGitignore: &policy.RespectGitignore, Exclude: policy.Exclude, Outputs: file.Outputs.Paths, PrivatePaths: []string{filepath.Dir(o.Store.ConfigFile), filepath.Dir(o.Store.IndexFile)}})
        if err != nil {
            return err
        }
        view := syncResult{DryRun: preview, Root: plan.Root, Policy: plan.Policy, SelectionSource: plan.Source, SelectedFiles: len(plan.Files), SelectedBytes: plan.Bytes, Excluded: plan.Excluded}
        view.Files = plan.Files
        result(view)
        return nil
    }
    return cmd
}

func printSync(out io.Writer, r syncResult, l *i18n.Localizer) {
    if r.DryRun {
        fmt.Fprintln(out, l.Text("sync_preview"))
    } else {
        fmt.Fprintln(out, l.Text("sync_done"))
    }
    fmt.Fprintln(out, l.Text("sync_selection", map[string]any{"Count": r.SelectedFiles, "Bytes": r.SelectedBytes}))
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
