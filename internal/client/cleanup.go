package client

import (
    "context"
    "errors"

    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
)

// Outputs observes metadata without starting a process or deploying implicitly.
func (c *Projects) OutputsList(ctx context.Context, id string, depth, limit int, prepare bool, policy CallPolicy) (execution.OutputListing, error) {
    if depth < 0 || depth > 20 || limit < 1 || limit > 1000 {
        return execution.OutputListing{}, fault.Invalid("--depth/--limit", "out_of_range", "outputs_bounds")
    }
    if c.Runs == nil || c.Outputs == nil {
        return execution.OutputListing{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, _, err := c.boundProject(ctx)
    if err != nil {
        return execution.OutputListing{}, err
    }
    ref, err := c.Runs.Select(project.ID, id)
    if err != nil {
        return execution.OutputListing{}, err
    }
    return c.Outputs.List(ctx, ref, depth, limit, prepare, policy.Batch)
}

// CleanInput selects explicit project-scoped IDs for an eligibility check.
// PrepareRunner permits helper preparation; it never authorizes payload deletion.
type CleanInput struct {
    IDs           []string
    PrepareRunner bool
    Policy        CallPolicy
}

// CleanBatch preserves each run's cleanup outcome independently.
type CleanBatch struct {
    DryRun bool                    `json:"dry_run"`
    Runs   []execution.CleanResult `json:"runs"`
}

// PrepareClean validates the complete explicit ID set without deleting payloads.
// A caller must obtain any required consent before passing this batch to ExecuteClean.
func (c *Projects) PrepareClean(ctx context.Context, in CleanInput) (CleanBatch, error) {
    if len(in.IDs) == 0 || len(in.IDs) > 1000 {
        return CleanBatch{}, fault.New("BAD_CONFIG", "argument_count", map[string]any{"Command": "loaf clean"})
    }
    if c.Runs == nil || c.Cleaner == nil {
        return CleanBatch{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, _, err := c.boundProject(ctx)
    if err != nil {
        return CleanBatch{}, err
    }
    batch := CleanBatch{DryRun: true, Runs: []execution.CleanResult{}}
    seen := map[string]bool{}
    var first error
    for _, id := range in.IDs {
        if seen[id] {
            continue
        }
        seen[id] = true
        ref, err := c.Runs.Select(project.ID, id)
        if err != nil {
            batch.Runs = append(batch.Runs, execution.CleanResult{Run: execution.View{ID: id}, ErrorCode: cleanErrorCode(err)})
            if first == nil {
                first = err
            }
            continue
        }
        item, err := c.Cleaner.Preview(ctx, ref, in.Policy.Batch, in.PrepareRunner)
        if err != nil {
            item.ErrorCode = cleanErrorCode(err)
            if first == nil {
                first = err
            }
        }
        batch.Runs = append(batch.Runs, item)
    }
    return batch, first
}

// ExecuteClean revalidates each prepared run at the remote cleanup boundary.
// Cancellation preserves complete per-ID coverage and never implies remote deletion.
func (c *Projects) ExecuteClean(ctx context.Context, batch CleanBatch, policy CallPolicy) (CleanBatch, error) {
    if len(batch.Runs) == 0 || len(batch.Runs) > 1000 {
        return batch, fault.New("BAD_CONFIG", "argument_count", map[string]any{"Command": "loaf clean"})
    }
    for _, item := range batch.Runs {
        if item.ErrorCode != "" || !item.Eligible {
            return batch, fault.New("CLEAN_NOT_TERMINAL", "clean_not_terminal", nil)
        }
    }
    if c.Cleaner == nil {
        return batch, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    batch.DryRun = false
    batch.Runs = append([]execution.CleanResult(nil), batch.Runs...)
    var first error
    for i, item := range batch.Runs {
        if ctx.Err() != nil {
            item.ErrorCode = "CLEAN_NOT_ATTEMPTED"
            batch.Runs[i] = item
            first = fault.Wrap("CLEAN_UNCONFIRMED", "clean_unconfirmed", ctx.Err())
            continue
        }
        cleaned, err := c.Cleaner.Clean(ctx, item, policy.Batch)
        if err != nil {
            cleaned.ErrorCode = cleanErrorCode(err)
            if first == nil || cleanErrorCode(err) == "CLEAN_UNCONFIRMED" {
                first = err
            }
        }
        batch.Runs[i] = cleaned
    }
    return batch, first
}

func cleanErrorCode(err error) string {
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return "CLEAN_FAILED"
}
