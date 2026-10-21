package cli

import (
    "fmt"
    "io"
    "sort"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/spf13/cobra"
)

type targetEntry struct {
    Name string `json:"name"`
    config.Target
    Selected bool `json:"selected"`
}

type targetChoice struct {
    Root      string `json:"root"`
    ProjectID string `json:"project_id,omitempty"`
    Target    string `json:"target,omitempty"`
    Source    string `json:"source"`
    Available bool   `json:"available"`
}

type targetsResult struct {
    Targets []targetEntry `json:"targets"`
    Project targetChoice  `json:"project"`
}

type removeResult struct {
    Target   string `json:"target"`
    Selected bool   `json:"selected"`
}

func projectChoice(cmd *cobra.Command, o Options, cfg config.Global) (targetChoice, error) {
    root, file, err := config.Discover(cmd.Context(), o.CWD)
    if err != nil {
        return targetChoice{}, err
    }
    personal, err := o.Store.Existing(root)
    if err != nil {
        return targetChoice{}, err
    }
    choice := targetChoice{Root: root, ProjectID: personal.ID, Target: file.Target, Source: "none"}
    if file.Target != "" {
        choice.Source = "project"
    }
    if personal.Target != "" {
        choice.Target, choice.Source = personal.Target, "personal"
    }
    _, choice.Available = cfg.Targets[choice.Target]
    return choice, nil
}

func newTargetCommands(o Options, cfg config.Global, l *i18n.Localizer, result func(any)) []*cobra.Command {
    targets := &cobra.Command{Use: "targets " + l.Text("targets_name_arg"), Short: l.Text("targets_short"), Args: arguments(0, 1)}
    targets.RunE = func(cmd *cobra.Command, args []string) error {
        if cmd.Flags().Changed("target") {
            return fault.New("BAD_CONFIG", "targets_flag_target", nil)
        }
        choice, err := projectChoice(cmd, o, cfg)
        if err != nil {
            return err
        }
        names := make([]string, 0, len(cfg.Targets))
        if len(args) == 1 {
            if _, ok := cfg.Targets[args[0]]; !ok {
                return fault.New("BAD_CONFIG", "target_unknown", map[string]any{"Target": args[0]})
            }
            names = append(names, args[0])
        } else {
            for name := range cfg.Targets {
                names = append(names, name)
            }
            sort.Strings(names)
        }
        data := targetsResult{Targets: []targetEntry{}, Project: choice}
        for _, name := range names {
            data.Targets = append(data.Targets, targetEntry{Name: name, Target: cfg.Targets[name], Selected: choice.Target == name})
        }
        result(data)
        return nil
    }
    remove := &cobra.Command{Use: "remove " + l.Text("name_arg"), Short: l.Text("remove_short"), Args: arguments(1, 1)}
    remove.RunE = func(cmd *cobra.Command, args []string) error {
        if cmd.Flags().Changed("target") {
            return fault.New("BAD_CONFIG", "targets_flag_target", nil)
        }
        choice, err := projectChoice(cmd, o, cfg)
        if err != nil {
            return err
        }
        if err := o.Store.Remove(cmd.Context(), args[0]); err != nil {
            return err
        }
        result(removeResult{Target: args[0], Selected: choice.Target == args[0]})
        return nil
    }
    return []*cobra.Command{targets, remove}
}

func printTargets(out io.Writer, data targetsResult, l *i18n.Localizer) {
    if data.Project.Target == "" {
        fmt.Fprintln(out, l.Text("targets_no_choice"))
    } else if !data.Project.Available {
        fmt.Fprintln(out, l.Text("targets_missing_choice", map[string]any{"Target": data.Project.Target}))
    } else {
        fmt.Fprintln(out, l.Text("targets_choice", map[string]any{"Target": data.Project.Target}))
    }
    if len(data.Targets) == 0 {
        fmt.Fprintln(out, l.Text("targets_empty"))
    }
    for _, entry := range data.Targets {
        marker := " "
        if entry.Selected {
            marker = "*"
        }
        fmt.Fprintf(out, "%s %s\n  %s: %s\n  %s: %s\n", marker, entry.Name, l.Text("targets_destination"), entry.Destination, l.Text("targets_remote_base"), entry.RemoteBase)
        if len(entry.Args) > 0 {
            fmt.Fprintf(out, "  %s: %q\n", l.Text("targets_ssh_args"), entry.Args)
        }
    }
}
