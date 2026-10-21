package cli

import (
    "encoding/json"
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

type runList = execution.History

func newRunCommands(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any), streamed *bool) []*cobra.Command {
    run := &cobra.Command{Use: "run " + l.Text("run_args"), Short: l.Text("run_short")}
    var wait string
    run.Flags().StringVar(&wait, "wait", "0s", l.Text("flag_wait"))
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
        layers := []string{cfg.Run.Wait, file.Run.Wait}
        if cmd.Flags().Changed("wait") {
            layers = append(layers, wait)
        }
        budget, err := config.ResolveWait(layers...)
        if err != nil {
            return err
        }
        tail, err := config.ResolveTail(cfg.Logs, file.Logs)
        if err != nil {
            return err
        }
        policy, err := config.ResolveSync(cfg.Sync, file.Sync)
        if err != nil {
            return err
        }
        pullPolicy, err := config.ResolvePull(cfg.Pull, file.Pull)
        if err != nil {
            return err
        }
        resultsDir, err := config.PullBase(root, pullPolicy)
        if err != nil {
            return err
        }
        plan, err := sources.Select(cmd.Context(), root, sources.SelectOptions{Mode: policy.Mode, RespectGitignore: &policy.RespectGitignore, Exclude: policy.Exclude, Outputs: file.Outputs.Paths, ResultsDir: resultsDir, PrivatePaths: []string{filepath.Dir(o.Store.ConfigFile), filepath.Dir(o.Store.IndexFile)}})
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
        submitted, err := o.Runs.Submit(cmd.Context(), execution.SubmitInput{Project: personal, File: file, Target: name, Connection: machine, RemoteDir: remoteDir, Plan: plan, Args: args, Batch: jsonMode || !o.Interactive}, func(stage string) {
            if !jsonMode {
                fmt.Fprintln(o.Err, l.Text("run_progress_"+stage))
            }
        })
        if submitted.ID != "" {
            result(submitted)
        }
        if err != nil {
            return err
        }
        if budget == 0 {
            return nil
        }
        if jsonMode {
            observed, err := o.Runs.Observe(cmd.Context(), submitted.Ref, budget, tail, true)
            result(observed)
            return err
        }
        fmt.Fprintln(o.Err, l.Text("run_accepted", map[string]any{"ID": submitted.ID}))
        phase := ""
        observed, err := o.Runs.Follow(cmd.Context(), submitted.Ref, budget, tail, !o.Interactive, func(v execution.View, kind string) error {
            if v.Phase != "" && v.Phase != phase {
                phase = v.Phase
                fmt.Fprintln(o.Err, l.Text("run_phase_"+phase))
            }
            if v.Log != nil {
                _, err := io.WriteString(o.Out, v.Log.Text)
                return err
            }
            return nil
        })
        observed.Log = nil
        result(observed)
        return err
    }
    ps := newPSCommand(o, cfg, target, l, result)
    logs := newLogsCommand(o, cfg, target, l, result, streamed)
    return []*cobra.Command{run, ps, logs, newStopCommand(o, cfg, target, l, result)}
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
    var wait, since, until, listTarget string
    var limit int
    var statuses []string
    cmd := &cobra.Command{Use: "ps " + l.Text("run_id_arg"), Short: l.Text("ps_short"), Args: arguments(0, 1)}
    cmd.Flags().StringVar(&wait, "wait", "0s", l.Text("flag_ps_wait"))
    cmd.Flags().IntVar(&limit, "limit", 20, l.Text("flag_limit"))
    cmd.Flags().StringArrayVar(&statuses, "status", nil, l.Text("ps_flag_status"))
    cmd.Flags().StringVar(&since, "since", "", l.Text("ps_flag_since"))
    cmd.Flags().StringVar(&until, "until", "", l.Text("ps_flag_until"))
    // A local flag replaces the inherited selector only for list filtering. It
    // never changes a saved run's connection and is rejected with a run ID.
    cmd.Flags().StringVar(&listTarget, "target", "", l.Text("ps_flag_target"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        budget, err := config.ResolveWait(wait)
        if err != nil {
            return err
        }
        if len(args) == 0 && budget > 0 {
            return fault.New("BAD_CONFIG", "invalid_flags", nil)
        }
        listFlags := []string{"status", "since", "until", "target", "limit"}
        if len(args) == 1 {
            for _, flag := range listFlags {
                if cmd.Flags().Changed(flag) {
                    return fault.New("BAD_CONFIG", "ps_list_only", nil)
                }
            }
        }
        if o.Runs == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        root, file, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        project, err := o.Store.Existing(root)
        if err != nil {
            return err
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        if len(args) == 1 {
            ref, err := o.Runs.Select(project.ID, args[0])
            if err != nil {
                return err
            }
            tail := 0
            if budget > 0 {
                tail, err = config.ResolveTail(cfg.Logs, file.Logs)
                if err != nil {
                    return err
                }
            }
            observed, err := o.Runs.Observe(cmd.Context(), ref, budget, tail, jsonMode || !o.Interactive)
            if budget == 0 {
                observed.Log = nil
            }
            result(observed)
            return err
        }
        layers := []config.PSSettings{cfg.PS, file.PS}
        if cmd.Flags().Changed("limit") {
            layers = append(layers, config.PSSettings{Limit: &limit})
        }
        maximum, err := config.ResolvePSLimit(layers...)
        if err != nil {
            return err
        }
        filters := execution.HistoryFilters{Statuses: statuses, Target: listTarget}
        for _, boundary := range []struct {
            value   string
            changed bool
            dst     **time.Time
        }{
            {since, cmd.Flags().Changed("since"), &filters.Since},
            {until, cmd.Flags().Changed("until"), &filters.Until},
        } {
            if !boundary.changed {
                continue
            }
            value, err := time.Parse(time.RFC3339Nano, boundary.value)
            if err != nil {
                return fault.New("BAD_CONFIG", "ps_invalid_time", nil)
            }
            value = value.UTC()
            *boundary.dst = &value
        }
        if cmd.Flags().Changed("target") && !config.ValidName(listTarget) {
            return fault.New("BAD_CONFIG", "invalid_target", nil)
        }
        list, err := o.Runs.History(cmd.Context(), project.ID, filters, maximum, jsonMode || !o.Interactive)
        result(list)
        return err
    }
    return cmd
}

func newLogsCommand(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any), streamed *bool) *cobra.Command {
    var tail int
    var follow bool
    cmd := &cobra.Command{Use: "logs " + l.Text("run_id_arg"), Short: l.Text("logs_short"), Args: arguments(0, 1)}
    cmd.Flags().IntVar(&tail, "tail", 100, l.Text("flag_tail"))
    cmd.Flags().BoolVarP(&follow, "follow", "f", false, l.Text("flag_follow"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        project, file, err := runProject(cmd, o, target)
        if err != nil {
            return err
        }
        layers := []config.LogSettings{cfg.Logs, file.Logs}
        if cmd.Flags().Changed("tail") {
            layers = append(layers, config.LogSettings{Tail: &tail})
        }
        lines, err := config.ResolveTail(layers...)
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
        if follow {
            if jsonMode {
                *streamed = true
            }
            last, err := o.Runs.Follow(cmd.Context(), ref, 0, lines, jsonMode || !o.Interactive, func(v execution.View, kind string) error {
                if jsonMode {
                    return json.NewEncoder(o.Out).Encode(envelope{SchemaVersion: 1, OK: true, Data: struct {
                        Type string         `json:"type"`
                        Run  execution.View `json:"run"`
                    }{kind, v}})
                }
                if v.Log != nil {
                    _, err := io.WriteString(o.Out, v.Log.Text)
                    return err
                }
                return nil
            })
            if err != nil && jsonMode {
                result(struct {
                    Type string         `json:"type"`
                    Run  execution.View `json:"run"`
                }{"error", last})
            }
            return err
        }
        observed, err := o.Runs.Observe(cmd.Context(), ref, 0, lines, jsonMode || !o.Interactive)
        if jsonMode {
            result(observed)
        } else if observed.Log != nil {
            if _, writeErr := io.WriteString(o.Out, observed.Log.Text); writeErr != nil {
                return writeErr
            }
            if observed.Log.Truncated {
                fmt.Fprintln(o.Err, l.Text("logs_truncated"))
            }
        }
        return err
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
    if v.Log != nil {
        fmt.Fprint(out, v.Log.Text)
        if v.Log.Truncated {
            fmt.Fprintln(out, l.Text("logs_truncated"))
        }
    }
}
