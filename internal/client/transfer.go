package client

import (
    "context"
    "path"
    "sync"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
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
    multi, concurrency, err := pullOptions(in)
    if err != nil {
        return PullResult{}, err
    }
    if c.Runs == nil || c.Puller == nil {
        return PullResult{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    plan, err := c.PreviewPull(ctx, in)
    if err != nil {
        if plan.failure != nil {
            return PullResult{Batch: plan.failure}, err
        }
        return PullResult{}, err
    }
    if multi {
        batch, err := c.batchPull(ctx, plan, concurrency, in.Policy.Batch, emit)
        return PullResult{Batch: &batch}, err
    }
    ref, selected := plan.refs[0], plan.Runs[0]
    progress(emit, Progress{Stage: "pull", Target: ref.Target, RunID: ref.ID})
    pulled, err := c.Puller.Pull(ctx, ref, selected.Destination, selected.Paths, plan.Preferences.Policy, in.Policy.Batch)
    return PullResult{Single: &pulled}, err
}

func (c *Projects) batchPull(ctx context.Context, plan PullPreview, concurrency int, batch bool, emit ProgressFunc) (PullBatch, error) {
    refs, policy := plan.refs, plan.Preferences.Policy
    result := PullBatch{Runs: make([]PullRow, len(refs)), Selected: len(refs)}
    for i, row := range plan.Runs {
        result.Runs[i].Result = sources.PullResult{RunID: row.ID, Target: row.Target, Destination: row.Destination, Policy: policy, Paths: row.Paths, DownloadedPaths: []string{}, MissingPaths: []string{}}
    }
    examined := make([]bool, len(refs))
    jobs := make(chan int)
    var workers sync.WaitGroup
    var progressMu sync.Mutex
    for range min(concurrency, len(refs)) {
        workers.Go(func() {
            for i := range jobs {
                if ctx.Err() != nil {
                    continue
                }
                progressMu.Lock()
                progress(emit, Progress{Stage: "pull", Target: refs[i].Target, RunID: refs[i].ID})
                progressMu.Unlock()
                row := plan.Runs[i]
                pulled, err := c.Puller.Pull(ctx, refs[i], row.Destination, row.Paths, policy, batch)
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
