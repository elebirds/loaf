package client

import (
    "context"
    "path/filepath"
    "sort"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
)

// AddResult is the saved static target identity.
type AddResult struct {
    Target      string `json:"target"`
    Destination string `json:"destination"`
    RemoteBase  string `json:"remote_base"`
}

// UseResult identifies the project-local target selection.
type UseResult struct {
    Target    string `json:"target"`
    ProjectID string `json:"project_id"`
    Root      string `json:"root"`
}

// TargetEntry is a named, locally configured target.
type TargetEntry struct {
    Name string `json:"name"`
    config.Target
    Selected bool `json:"selected"`
}

// TargetChoice reports project preference without creating one.
type TargetChoice struct {
    Root      string `json:"root"`
    ProjectID string `json:"project_id,omitempty"`
    Target    string `json:"target,omitempty"`
    Source    string `json:"source"`
    Available bool   `json:"available"`
}

// TargetsResult contains local configuration only.
type TargetsResult struct {
    Targets []TargetEntry `json:"targets"`
    Project TargetChoice  `json:"project"`
}

// RemoveResult reports alias removal without altering historical run bindings.
type RemoveResult struct {
    Target   string `json:"target"`
    Selected bool   `json:"selected"`
}

// AddTarget parses a connection without interpreting appended shell commands.
func (c *Machines) AddTarget(ctx context.Context, name, connection, remote string, replace bool) (AddResult, error) {
    target, err := ssh.ParseConnection(connection)
    if err != nil {
        return AddResult{}, err
    }
    target.RemoteBase = remote
    if err := c.Config.Add(ctx, name, target, replace); err != nil {
        return AddResult{}, err
    }
    return AddResult{name, target.Destination, target.RemoteBase}, nil
}

// UseTarget records a choice only for the discovered project.
func (c *Machines) UseTarget(ctx context.Context, name string) (UseResult, error) {
    settings, err := c.Config.Load()
    if err != nil {
        return UseResult{}, err
    }
    if _, ok := settings.Targets[name]; !ok {
        return UseResult{}, fault.New("BAD_CONFIG", "target_unknown", map[string]any{"Target": name})
    }
    root, _, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return UseResult{}, err
    }
    project, err := c.State.Select(ctx, root, name)
    return UseResult{name, project.ID, project.Root}, err
}

func (c *Machines) projectChoice(ctx context.Context) (TargetChoice, error) {
    root, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return TargetChoice{}, err
    }
    personal, err := c.State.Existing(root)
    if err != nil {
        return TargetChoice{}, err
    }
    choice := TargetChoice{Root: root, ProjectID: personal.ID, Target: file.Target, Source: "none"}
    if file.Target != "" {
        choice.Source = "project"
    }
    if personal.Target != "" {
        choice.Target, choice.Source = personal.Target, "personal"
    }
    _, choice.Available = c.Settings.Targets[choice.Target]
    return choice, nil
}

// Targets returns sorted aliases and the unmodified project preference.
func (c *Machines) Targets(ctx context.Context, name string) (TargetsResult, error) {
    choice, err := c.projectChoice(ctx)
    if err != nil {
        return TargetsResult{}, err
    }
    names := make([]string, 0, len(c.Settings.Targets))
    if name != "" {
        if _, ok := c.Settings.Targets[name]; !ok {
            return TargetsResult{}, fault.New("BAD_CONFIG", "target_unknown", map[string]any{"Target": name})
        }
        names = append(names, name)
    } else {
        for name := range c.Settings.Targets {
            names = append(names, name)
        }
        sort.Strings(names)
    }
    result := TargetsResult{Targets: []TargetEntry{}, Project: choice}
    for _, name := range names {
        result.Targets = append(result.Targets, TargetEntry{Name: name, Target: c.Settings.Targets[name], Selected: choice.Target == name})
    }
    return result, nil
}

// RemoveTarget removes an alias, not its historical runs or project TOML.
func (c *Machines) RemoveTarget(ctx context.Context, name string) (RemoveResult, error) {
    choice, err := c.projectChoice(ctx)
    if err != nil {
        return RemoveResult{}, err
    }
    if err := c.Config.Remove(ctx, name); err != nil {
        return RemoveResult{}, err
    }
    return RemoveResult{Target: name, Selected: choice.Target == name}, nil
}

func (c *Projects) boundProject(ctx context.Context) (config.Project, config.ProjectFile, error) {
    root, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return config.Project{}, file, err
    }
    personal, err := c.State.Existing(root)
    return personal, file, err
}

func (c *Machines) machine(ctx context.Context, explicit string) (string, config.Target, config.ProjectFile, error) {
    root, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return "", config.Target{}, file, err
    }
    project, err := c.State.Existing(root)
    if err != nil {
        return "", config.Target{}, file, err
    }
    name, target, err := config.Resolve(explicit, project, file, c.Settings)
    return name, target, file, err
}

func (c *Projects) sourcePlan(ctx context.Context, root string, file config.ProjectFile, policy config.SyncPolicy, pull config.PullPolicy) (sources.Plan, error) {
    resultBase, err := config.PullBase(root, pull)
    if err != nil {
        return sources.Plan{}, err
    }
    return sources.Select(ctx, root, sources.SelectOptions{Mode: policy.Mode, RespectGitignore: &policy.RespectGitignore, Exclude: policy.Exclude, Outputs: file.Outputs.Paths, ResultsDir: resultBase, PrivatePaths: []string{filepath.Dir(c.Config.ConfigFile), c.State.Dir}})
}
