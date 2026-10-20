package cli

import (
    "context"
    "fmt"
    "io"
    "path"
    "path/filepath"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/spf13/cobra"
)

type runList struct {
    Runs      []execution.View `json:"runs"`
    Total     int              `json:"total"`
    Truncated bool             `json:"truncated"`
}

func newRunCommands(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any)) []*cobra.Command {
    run := &cobra.Command{Use: "run " + l.Text("run_args"), Short: l.Text("run_short")}
    run.Args = func(cmd *cobra.Command, args []string) error {
        if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
            return fault.New("BAD_CONFIG", "run_command", nil)
        }
        return nil
    }
    run.RunE = func(cmd *cobra.Command, args []string) error {
        root, file, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        policy, err := config.ResolveSync(cfg.Sync, file.Sync)
        if err != nil {
            return err
        }
        plan, err := sources.Select(cmd.Context(), root, sources.SelectOptions{Mode: policy.Mode, RespectGitignore: &policy.RespectGitignore, Exclude: policy.Exclude, Outputs: file.Outputs.Paths, PrivatePaths: []string{filepath.Dir(o.Store.ConfigFile), filepath.Dir(o.Store.IndexFile)}})
        if err != nil {
            return err
        }
        personal, err := o.Store.Existing(root)
        if err != nil {
            return err
        }
        name, machine, err := config.Resolve(*target, personal, file, cfg)
        if err != nil {
            return err
        }
        if o.Runs == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        personal, err = o.Store.Select(cmd.Context(), root, "")
        if err != nil {
            return err
        }
        remoteDir := file.RemoteDir
        if remoteDir == "" {
            remoteDir = path.Join(machine.RemoteBase, personal.ID)
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        if !jsonMode && policy.Mode == "update" {
            fmt.Fprintln(o.Err, l.Text("sync_update_warning"))
        }
        submitted, err := o.Runs.Submit(cmd.Context(), execution.SubmitInput{Project: personal, File: file, Target: name, Connection: machine, RemoteDir: remoteDir, Plan: plan, Args: args, Batch: jsonMode || !o.Interactive}, nil)
        if submitted.ID != "" {
            result(submitted)
        }
        if err != nil {
            return err
        }
        return nil
    }
    ps := newPSCommand(o, cfg, target, l, result)
    return []*cobra.Command{run, ps}
}

func runProject(cmd *cobra.Command, o Options, target *string) (config.Project, config.ProjectFile, error) {
    if cmd.Flags().Changed("target") {
        return config.Project{}, config.ProjectFile{}, fault.New("BAD_CONFIG", "run_bound_target", nil)
    }
    if o.Runs == nil {
        return config.Project{}, config.ProjectFile{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    root, file, err := config.Discover(cmd.Context(), o.CWD)
    if err != nil {
        return config.Project{}, file, err
    }
    personal, err := o.Store.Existing(root)
    return personal, file, err
}

func newPSCommand(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any)) *cobra.Command {
    var limit int
    cmd := &cobra.Command{Use: "ps " + l.Text("run_id_arg"), Short: l.Text("ps_short"), Args: arguments(0, 1)}
    cmd.Flags().IntVar(&limit, "limit", 20, l.Text("flag_limit"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        if limit < 1 || limit > 1000 {
            return fault.New("BAD_CONFIG", "invalid_flags", nil)
        }
        project, _, err := runProject(cmd, o, target)
        if err != nil {
            return err
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        if len(args) == 1 {
            ref, err := o.Runs.Select(project.ID, args[0])
            if err != nil {
                return err
            }
            observed, err := o.Runs.Observe(cmd.Context(), ref, jsonMode || !o.Interactive)
            result(observed)
            return err
        }
        refs, err := o.Runs.Index.Runs(project.ID)
        if err != nil {
            return err
        }
        list := runList{Runs: []execution.View{}, Total: len(refs), Truncated: len(refs) > limit}
        ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
        defer cancel()
        failed := false
        for _, ref := range refs[:min(len(refs), limit)] {
            observed, err := o.Runs.Observe(ctx, ref, jsonMode || !o.Interactive)
            if err != nil {
                failed = true
            }
            list.Runs = append(list.Runs, observed)
        }
        result(list)
        if failed {
            return fault.New("QUERY_PARTIAL", "run_query_partial", nil)
        }
        return nil
    }
    return cmd
}

func printRun(out io.Writer, v execution.View, l *i18n.Localizer) {
    state := v.Status
    switch state {
    case "starting", "running", "succeeded", "failed", "stopped", "unknown":
        state = l.Text("run_status_" + state)
    }
    fmt.Fprintf(out, "%s  %s  %s", v.ID, v.Target, state)
    if v.Phase != "" {
        fmt.Fprintf(out, "  %s", l.Text("run_phase_"+v.Phase))
    }
    if v.ExitCode != nil {
        fmt.Fprintf(out, "  %s", l.Text("run_exit", map[string]any{"Value": *v.ExitCode}))
    }
    if v.Signal != "" {
        fmt.Fprintf(out, "  %s", v.Signal)
    }
    if v.Reason != "" {
        fmt.Fprintf(out, "  %s", l.Text("run_reason_"+v.Reason))
    }
    if v.QueryError != "" {
        fmt.Fprintf(out, "  %s: %s", l.Text("run_query"), v.QueryError)
    }
    fmt.Fprintln(out)
}
