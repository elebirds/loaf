package client

import (
    "context"
    "path"
    "path/filepath"
    "sync"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

// SyncInput contains source-policy overrides; preview performs no remote work.
type SyncInput struct {
    Target           *string
    DryRun           bool
    Mode             *string
    RespectGitignore *bool
    Exclude          []string
    Policy           CallPolicy
}

// SyncResult describes local selection and, when performed, transfer observation.
type SyncResult struct {
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

// Sync selects working files, resolves one target and performs one transfer.
func (c *Projects) Sync(ctx context.Context, in SyncInput, emit ProgressFunc) (result SyncResult, err error) {
    defer func() {
        if err != nil && ctx.Err() != nil {
            failure := fault.Wrap("SYNC_CANCELED", "sync_canceled", ctx.Err())
            if !in.DryRun {
                failure.Hint = "sync_retry"
            }
            err = failure
        }
    }()
    root, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return result, err
    }
    policy, err := config.ResolveSync(c.Settings.Sync, file.Sync, config.SyncSettings{Mode: value(in.Mode), RespectGitignore: in.RespectGitignore, Exclude: in.Exclude})
    if err != nil {
        return result, err
    }
    pull, err := config.ResolvePull(c.Settings.Pull, file.Pull)
    if err != nil {
        return result, err
    }
    plan, err := c.sourcePlan(ctx, root, file, policy, pull)
    if err != nil {
        return result, err
    }
    result = SyncResult{DryRun: in.DryRun, Root: plan.Root, Policy: plan.Policy, SelectionSource: plan.Source, SelectedFiles: len(plan.Files), SelectedBytes: plan.Bytes, Excluded: plan.Excluded}
    if in.DryRun {
        result.Files = plan.Files
        return result, nil
    }
    personal, err := c.State.Existing(plan.Root)
    if err != nil {
        return result, err
    }
    name, machine, err := config.Resolve(value(in.Target), personal, file, c.Settings)
    if err != nil {
        return result, err
    }
    if c.Syncer == nil {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    personal, err = c.State.Select(ctx, plan.Root, "")
    if err != nil {
        return result, err
    }
    remote := file.RemoteDir
    if remote == "" {
        remote = path.Join(machine.RemoteBase, personal.ID)
    }
    progress(emit, Progress{Stage: "sync", Target: name, Count: result.SelectedFiles})
    synced, err := c.Syncer.Sync(ctx, machine, personal.ID, remote, plan, in.Policy.Batch)
    if err != nil {
        return result, err
    }
    result.Target, result.ProjectID, result.RemoteDir = name, personal.ID, synced.RemoteDir
    result.ChangedFiles = &synced.ChangedFiles
    return result, nil
}

// PullInput retains literal paths and optional destination/checksum overrides.
type PullInput struct {
    IDs         []string
    Paths       []string
    To, Into    *string
    Checksum    *bool
    Concurrency *int
    Policy      CallPolicy
}

// PullRow retains an operation error without localizing it in a worker.
type PullRow struct {
    Result sources.PullResult
    Error  error
}

// PullBatch reports complete partial coverage of a frozen input set.
type PullBatch struct {
    Runs                                    []PullRow
    Selected, Completed, Failed, Unexamined int
}

// PullResult distinguishes exact single destination from isolated batch destinations.
type PullResult struct {
    Single *sources.PullResult
    Batch  *PullBatch
}

// Pull validates all selections before transfer and never mirrors local deletion.
func (c *Projects) Pull(ctx context.Context, in PullInput, emit ProgressFunc) (PullResult, error) {
    multi := len(in.IDs) > 1 || in.Into != nil
    concurrency := 1
    if in.Concurrency != nil {
        concurrency = *in.Concurrency
    }
    if (multi && (value(in.Into) == "" || len(in.IDs) == 0 || in.To != nil)) || (!multi && in.Concurrency != nil) || concurrency < 1 || concurrency > 4 || (concurrency > 1 && !in.Policy.Batch) {
        return PullResult{}, fault.New("BAD_CONFIG", "pull_batch_flags", nil)
    }
    if c.Runs == nil || c.Puller == nil {
        return PullResult{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, file, err := c.boundProject(ctx)
    if err != nil {
        return PullResult{}, err
    }
    policy, err := config.ResolvePull(c.Settings.Pull, file.Pull, config.PullSettings{Checksum: in.Checksum})
    if err != nil {
        return PullResult{}, err
    }
    if in.To != nil && *in.To == "" {
        return PullResult{}, fault.New("BAD_CONFIG", "pull_destination", nil)
    }
    if multi {
        refs, err := c.Runs.Freeze(project.ID, in.IDs, nil)
        if err != nil {
            return PullResult{}, err
        }
        batch, err := c.batchPull(ctx, project, refs, value(in.Into), in.Paths, policy, concurrency, in.Policy.Batch)
        return PullResult{Batch: &batch}, err
    }
    id := ""
    if len(in.IDs) > 0 {
        id = in.IDs[0]
    }
    ref, err := c.Runs.Select(project.ID, id)
    if err != nil {
        return PullResult{}, err
    }
    selected := ref.Outputs
    if in.Paths != nil {
        selected = in.Paths
    }
    selected, err = sources.OutputPaths(selected)
    if err != nil {
        return PullResult{}, err
    }
    destination, err := sources.Destination(project.Root, c.CWD, value(in.To), ref.ID, policy)
    if err != nil {
        return PullResult{}, err
    }
    progress(emit, Progress{Stage: "pull", Target: ref.Target, RunID: ref.ID})
    pulled, err := c.Puller.Pull(ctx, ref, destination, selected, policy, in.Policy.Batch)
    return PullResult{Single: &pulled}, err
}

func (c *Projects) batchPull(ctx context.Context, project config.Project, refs []task.RunRef, into string, paths []string, policy config.PullPolicy, concurrency int, batch bool) (PullBatch, error) {
    parent, err := sources.Destination(project.Root, c.CWD, into, "", policy)
    if err != nil {
        return PullBatch{}, err
    }
    result := PullBatch{Runs: make([]PullRow, len(refs)), Selected: len(refs)}
    destinations := make([]string, len(refs))
    selections := make([][]string, len(refs))
    for i, ref := range refs {
        selected := ref.Outputs
        if paths != nil {
            selected = paths
        }
        selections[i], err = sources.OutputPaths(selected)
        if err != nil {
            return result, err
        }
        destinations[i], err = sources.Destination(project.Root, c.CWD, filepath.Join(parent, ref.ID), ref.ID, policy)
        if err != nil {
            return result, err
        }
        result.Runs[i].Result = sources.PullResult{RunID: ref.ID, Target: ref.Target, Destination: destinations[i], Policy: policy, Paths: selections[i], DownloadedPaths: []string{}, MissingPaths: []string{}}
    }
    examined := make([]bool, len(refs))
    jobs := make(chan int)
    var workers sync.WaitGroup
    for range min(concurrency, len(refs)) {
        workers.Go(func() {
            for i := range jobs {
                if ctx.Err() != nil {
                    continue
                }
                pulled, err := c.Puller.Pull(ctx, refs[i], destinations[i], selections[i], policy, batch)
                examined[i] = true
                result.Runs[i] = PullRow{Result: pulled, Error: err}
            }
        })
    }
    for i := range refs {
        jobs <- i
    }
    close(jobs)
    workers.Wait()
    for i, row := range result.Runs {
        if !examined[i] {
            result.Unexamined++
            result.Runs[i].Error = fault.New("NOT_EXAMINED", "pull_not_examined", nil)
        } else if row.Error != nil {
            result.Failed++
        } else {
            result.Completed++
        }
    }
    if result.Failed > 0 || result.Unexamined > 0 {
        return result, fault.New("PULL_PARTIAL", "pull_batch_partial", nil)
    }
    return result, nil
}
