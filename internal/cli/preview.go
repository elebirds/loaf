package cli

import (
    "fmt"
    "io"
    "path"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/spf13/cobra"
)

func previewRun(cmd *cobra.Command, root string, file config.ProjectFile, cfg config.Global, personal config.Project, name string, machine config.Target, args []string, metadata config.RunMetadata, plan sources.Plan, pull config.PullPolicy, budget time.Duration, tail int) (execution.RunPreview, error) {
    argv, keys, err := execution.PreviewCommand(file, args)
    if err != nil {
        return execution.RunPreview{}, err
    }
    outputs := file.Outputs.Paths
    if len(outputs) == 0 {
        outputs = []string{"outputs"}
    }
    outputs, err = sources.OutputPaths(outputs)
    if err != nil {
        return execution.RunPreview{}, err
    }
    source := func(flag string, project, user bool) []string {
        if flag != "" && cmd.Flags().Changed(flag) {
            return []string{"flag"}
        }
        if project {
            return []string{"project"}
        }
        if user {
            return []string{"user"}
        }
        return []string{"default"}
    }
    targetSource := "project"
    if personal.Target != "" {
        targetSource = "local_selection"
    }
    if cmd.Flags().Changed("target") {
        targetSource = "flag"
    }
    provenance := map[string][]string{
        "target": {targetSource}, "command": source("", len(args) == 0, false),
        "wait":                   source("wait", file.Run.Wait != "", cfg.Run.Wait != ""),
        "logs.tail":              source("", file.Logs.Tail != nil, cfg.Logs.Tail != nil),
        "sync.mode":              source("", file.Sync.Mode != "", cfg.Sync.Mode != ""),
        "sync.respect_gitignore": source("", file.Sync.RespectGitignore != nil, cfg.Sync.RespectGitignore != nil),
        "sync.exclude":           {"default"}, "outputs": source("", len(file.Outputs.Paths) > 0, false),
        "pull.base_dir": source("", file.Pull.BaseDir != "", cfg.Pull.BaseDir != ""),
        "pull.checksum": source("", file.Pull.Checksum != nil, cfg.Pull.Checksum != nil),
        "setup":         source("", file.Run.Setup != "", false), "environment_keys": source("", len(keys) > 0, false),
    }
    if len(args) > 0 {
        provenance["command"] = []string{"flag"}
    }
    if len(cfg.Sync.Exclude) > 0 {
        provenance["sync.exclude"] = append(provenance["sync.exclude"], "user")
    }
    if len(file.Sync.Exclude) > 0 {
        provenance["sync.exclude"] = append(provenance["sync.exclude"], "project")
    }
    if metadata.Name != "" {
        provenance["name"] = []string{"flag"}
    }
    if len(metadata.Tags) > 0 {
        provenance["tags"] = []string{"flag"}
    }
    remote := file.RemoteDir
    provenance["remote_directory"] = source("", remote != "", false)
    if remote == "" && personal.ID != "" {
        remote = path.Join(machine.RemoteBase, personal.ID)
    }
    return execution.RunPreview{DryRun: true, Root: root, Target: name, Args: argv, SetupPresent: file.Run.Setup != "", EnvironmentKeys: keys, RemoteBase: machine.RemoteBase, RemoteDirectory: remote, ExistingProjectID: personal.ID, WaitMillis: budget.Milliseconds(), Tail: tail, Outputs: outputs, Pull: pull, Source: plan, Metadata: metadata, Provenance: provenance}, nil
}

func printPreview(out io.Writer, r execution.RunPreview, l *i18n.Localizer) {
    fmt.Fprintln(out, l.Text("run_preview_heading"))
    fmt.Fprintln(out, l.Text("run_preview_target", map[string]any{"Target": r.Target, "Value": provenanceText(r.Provenance["target"], l)}))
    fmt.Fprintln(out, l.Text("run_preview_command", map[string]any{"Value": fmt.Sprintf("%q", r.Args)}))
    fmt.Fprintln(out, l.Text("run_preview_setup", map[string]any{"Value": r.SetupPresent}))
    fmt.Fprintln(out, l.Text("run_preview_location", map[string]any{"Root": r.RemoteBase, "Remote": r.RemoteDirectory}))
    fmt.Fprintln(out, l.Text("run_preview_pull", map[string]any{"Root": r.Pull.BaseDir, "Value": r.Pull.Checksum}))
    fmt.Fprintln(out, l.Text("run_preview_environment", map[string]any{"Value": r.EnvironmentKeys}))
    fmt.Fprintln(out, l.Text("run_preview_policies", map[string]any{"Millis": r.WaitMillis, "Count": r.Tail, "Mode": r.Source.Policy.Mode, "Paths": r.Outputs}))
    fmt.Fprintln(out, l.Text("run_preview_source", map[string]any{"Count": len(r.Source.Files), "Bytes": r.Source.Bytes, "Root": r.Root}))
    for _, field := range []string{"command", "wait", "logs.tail", "sync.mode", "sync.respect_gitignore", "sync.exclude", "outputs", "pull.base_dir", "pull.checksum"} {
        fmt.Fprintf(out, "  %s: %s\n", field, provenanceText(r.Provenance[field], l))
    }
    if r.Source.Policy.Mode == "update" {
        fmt.Fprintln(out, l.Text("sync_update_warning"))
    }
    fmt.Fprintln(out, l.Text("run_preview_not_snapshot"))
}

func provenanceText(sources []string, l *i18n.Localizer) string {
    labels := make([]string, len(sources))
    for i, source := range sources {
        labels[i] = l.Text("preview_source_" + source)
    }
    return strings.Join(labels, " + ")
}
