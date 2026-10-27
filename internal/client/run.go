package client

import (
    "context"
    "path"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

// RunInput retains explicit overrides separately from configuration defaults.
type RunInput struct {
    Target *string
    Wait   *string
    Args   []string
    Name   string
    Tags   []string
    DryRun bool
    Policy CallPolicy
}

// RunFeedback observes preparation and successful log consumption.
type RunFeedback struct {
    Progress ProgressFunc
    Log      func(execution.View, string) error
}

// RunResult is either an offline preview or the saved submission/observation.
type RunResult struct {
    Preview *execution.RunPreview
    Run     *execution.View
}

type runPlan struct {
    root      string
    file      config.ProjectFile
    project   config.Project
    target    string
    machine   config.Target
    source    sources.Plan
    pull      config.PullPolicy
    metadata  task.RunMetadata
    execution execution.ExecutionSpec
    wait      time.Duration
    tail      int
}

func (c *Projects) planRun(ctx context.Context, in RunInput) (runPlan, error) {
    var p runPlan
    var err error
    p.metadata, err = task.NormalizeRunMetadata(task.RunMetadata{Name: in.Name, Tags: in.Tags})
    if err != nil {
        return p, err
    }
    p.root, p.file, err = config.Discover(ctx, c.CWD)
    if err != nil {
        return p, err
    }
    p.execution, err = execution.ResolveExecution(p.file, in.Args)
    if err != nil {
        return p, err
    }
    waits := []string{c.Settings.Run.Wait, p.file.Run.Wait}
    if in.Wait != nil {
        waits = append(waits, *in.Wait)
    }
    p.wait, err = config.ResolveWait(waits...)
    if err != nil {
        return p, err
    }
    p.tail, err = config.ResolveTail(c.Settings.Logs, p.file.Logs)
    if err != nil {
        return p, err
    }
    policy, err := config.ResolveSync(c.Settings.Sync, p.file.Sync)
    if err != nil {
        return p, err
    }
    p.pull, err = config.ResolvePull(c.Settings.Pull, p.file.Pull)
    if err != nil {
        return p, err
    }
    p.source, err = c.sourcePlan(ctx, p.root, p.file, policy, p.pull)
    if err != nil {
        return p, err
    }
    p.project, err = c.State.Existing(p.root)
    if err != nil {
        return p, err
    }
    p.target, p.machine, err = config.Resolve(value(in.Target), p.project, p.file, c.Settings)
    return p, err
}

// Run plans locally, submits once, then optionally observes that same binding.
func (c *Projects) Run(ctx context.Context, in RunInput, feedback RunFeedback) (RunResult, error) {
    p, err := c.planRun(ctx, in)
    if err != nil {
        return RunResult{}, err
    }
    if in.DryRun {
        preview := c.previewRun(in, p)
        return RunResult{Preview: &preview}, nil
    }
    if c.Runs == nil {
        return RunResult{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    p.project, err = c.State.Select(ctx, p.root, "")
    if err != nil {
        return RunResult{}, err
    }
    remote := p.file.RemoteDir
    if remote == "" {
        remote = path.Join(p.machine.RemoteBase, p.project.ID)
    }
    if p.source.Policy.Mode == "update" {
        progress(feedback.Progress, Progress{Stage: "sync_update"})
    }
    submitted, err := c.Runs.Submit(ctx, execution.SubmitInput{Project: p.project, File: p.file, Target: p.target, Connection: p.machine, RemoteDir: remote, Plan: p.source, Args: in.Args, Metadata: p.metadata, Batch: in.Policy.Batch}, func(stage string) { progress(feedback.Progress, Progress{Stage: "run_" + stage}) })
    result := RunResult{}
    if submitted.ID != "" {
        result.Run = &submitted
    }
    if err != nil || p.wait == 0 {
        return result, err
    }
    if feedback.Log == nil {
        observed, err := c.Runs.Observe(ctx, submitted.Ref, p.wait, p.tail, in.Policy.Batch)
        result.Run = &observed
        return result, err
    }
    progress(feedback.Progress, Progress{Stage: "run_accepted", RunID: submitted.ID})
    observed, err := c.Runs.Follow(ctx, submitted.Ref, p.wait, p.tail, in.Policy.Batch, feedback.Log)
    observed.Log = nil // The sink already consumed the log; do not repeat it in the final view.
    result.Run = &observed
    return result, err
}

func (c *Projects) previewRun(in RunInput, p runPlan) execution.RunPreview {
    keys := p.execution.EnvironmentKeys()
    source := func(explicit, project, user bool) []string {
        switch {
        case explicit:
            return []string{"flag"}
        case project:
            return []string{"project"}
        case user:
            return []string{"user"}
        default:
            return []string{"default"}
        }
    }
    targetSource := "project"
    if p.project.Target != "" {
        targetSource = "local_selection"
    }
    if in.Target != nil {
        targetSource = "flag"
    }
    f, g := p.file, c.Settings
    provenance := map[string][]string{
        "target": {targetSource}, "command": source(len(in.Args) > 0, len(in.Args) == 0, false),
        "wait":                   source(in.Wait != nil, f.Run.Wait != "", g.Run.Wait != ""),
        "logs.tail":              source(false, f.Logs.Tail != nil, g.Logs.Tail != nil),
        "sync.mode":              source(false, f.Sync.Mode != "", g.Sync.Mode != ""),
        "sync.respect_gitignore": source(false, f.Sync.RespectGitignore != nil, g.Sync.RespectGitignore != nil),
        "sync.exclude":           {"default"}, "outputs": source(false, len(f.Outputs.Paths) > 0, false),
        "pull.base_dir": source(false, f.Pull.BaseDir != "", g.Pull.BaseDir != ""),
        "pull.checksum": source(false, f.Pull.Checksum != nil, g.Pull.Checksum != nil),
        "setup":         source(false, f.Run.Setup != "", false), "environment_keys": source(false, len(keys) > 0, false),
        "remote_directory": source(false, f.RemoteDir != "", false),
    }
    if len(g.Sync.Exclude) > 0 {
        provenance["sync.exclude"] = append(provenance["sync.exclude"], "user")
    }
    if len(f.Sync.Exclude) > 0 {
        provenance["sync.exclude"] = append(provenance["sync.exclude"], "project")
    }
    if p.metadata.Name != "" {
        provenance["name"] = []string{"flag"}
    }
    if len(p.metadata.Tags) > 0 {
        provenance["tags"] = []string{"flag"}
    }
    remote := f.RemoteDir
    if remote == "" && p.project.ID != "" {
        remote = path.Join(p.machine.RemoteBase, p.project.ID)
    }
    return execution.RunPreview{DryRun: true, Root: p.root, Target: p.target, Args: p.execution.Args, SetupPresent: p.execution.Setup != "", EnvironmentKeys: keys, RemoteBase: p.machine.RemoteBase, RemoteDirectory: remote, ExistingProjectID: p.project.ID, WaitMillis: p.wait.Milliseconds(), Tail: p.tail, Outputs: p.execution.Outputs, Pull: p.pull, Source: p.source, Metadata: p.metadata, Provenance: provenance}
}
