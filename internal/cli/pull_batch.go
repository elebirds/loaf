package cli

import (
    "context"
    "errors"
    "path/filepath"
    "sync"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
)

type pullRow struct {
    Result sources.PullResult `json:"result"`
    Error  *errorView         `json:"error,omitempty"`
}
type pullBatch struct {
    Runs       []pullRow `json:"runs"`
    Selected   int       `json:"selected"`
    Completed  int       `json:"completed"`
    Failed     int       `json:"failed"`
    Unexamined int       `json:"unexamined"`
}

func batchPull(ctx context.Context, o Options, project config.Project, refs []config.RunRef, into string, paths []string, override bool, policy config.PullPolicy, concurrency int, batch bool, l *i18n.Localizer) (pullBatch, error) {
    parent, err := sources.Destination(project.Root, o.CWD, into, "", policy)
    if err != nil {
        return pullBatch{}, err
    }
    into = parent
    result := pullBatch{Runs: make([]pullRow, len(refs)), Selected: len(refs)}
    destinations := make([]string, len(refs))
    selections := make([][]string, len(refs))
    for i, ref := range refs {
        selection := ref.Outputs
        if override {
            selection = paths
        }
        var err error
        selections[i], err = sources.OutputPaths(selection)
        if err != nil {
            return result, err
        }
        destinations[i], err = sources.Destination(project.Root, o.CWD, filepath.Join(into, ref.ID), ref.ID, policy)
        if err != nil {
            return result, err
        }
        result.Runs[i].Result = sources.PullResult{RunID: ref.ID, Target: ref.Target, Destination: destinations[i], Policy: policy, Paths: selections[i], DownloadedPaths: []string{}, MissingPaths: []string{}}
    }
    examined := make([]bool, len(refs))
    jobs := make(chan int)
    var workers sync.WaitGroup
    for range min(concurrency, len(refs)) {
        workers.Add(1)
        go func() {
            defer workers.Done()
            for i := range jobs {
                if ctx.Err() != nil {
                    continue
                }
                pulled, err := o.Puller.Pull(ctx, refs[i], destinations[i], selections[i], policy, batch)
                examined[i] = true
                result.Runs[i].Result = pulled
                if err != nil {
                    result.Runs[i].Error = batchError(err, l)
                }
            }
        }()
    }
    for i := range refs {
        jobs <- i
    }
    close(jobs)
    workers.Wait()
    for i, row := range result.Runs {
        if !examined[i] {
            result.Unexamined++
            result.Runs[i].Error = &errorView{Code: "NOT_EXAMINED", Message: l.Text("pull_not_examined")}
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

func executionErrorCode(err error) string {
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return "QUERY_FAILED"
}

// Batch errors retain stable codes and a complete localized message, without
// dumping arbitrary native diagnostics into every row.
func batchError(err error, l *i18n.Localizer) *errorView {
    result := &errorView{Code: executionErrorCode(err), Stage: fault.Stage(err), FailureKind: ssh.FailureKind(err), Message: l.Text("run_query")}
    var f *fault.Error
    if errors.As(err, &f) {
        result.Message = l.Text(f.Message, f.Params)
    }
    return result
}
