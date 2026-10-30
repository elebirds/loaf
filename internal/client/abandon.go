package client

import (
    "context"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
)

// AbandonInput names the complete explicit preparation-reclamation scope.
type AbandonInput struct {
    IDs           []string
    PrepareRunner bool
    Policy        CallPolicy
}

// AbandonBatch retains every selected ID and its independent no-start evidence.
type AbandonBatch struct {
    DryRun bool                      `json:"dry_run"`
    Runs   []execution.AbandonResult `json:"runs"`
}

// PrepareAbandon previews all IDs without mutating any run or granting consent.
func (c *Projects) PrepareAbandon(ctx context.Context, in AbandonInput) (AbandonBatch, error) {
    if len(in.IDs) == 0 || len(in.IDs) > 1000 {
        return AbandonBatch{}, fault.New("BAD_CONFIG", "argument_count", map[string]any{"Command": "loaf abandon"})
    }
    for _, id := range in.IDs {
        if id == "" {
            return AbandonBatch{}, fault.New("BAD_CONFIG", "argument_count", map[string]any{"Command": "loaf abandon"})
        }
    }
    if c.Runs == nil || c.Abandoner == nil {
        return AbandonBatch{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, _, err := c.boundProject(ctx)
    if err != nil {
        return AbandonBatch{}, err
    }
    batch := AbandonBatch{DryRun: true, Runs: []execution.AbandonResult{}}
    seen := map[string]bool{}
    var first error
    for _, id := range in.IDs {
        if seen[id] {
            continue
        }
        seen[id] = true
        ref, err := c.Runs.Select(project.ID, id)
        if err != nil {
            batch.Runs = append(batch.Runs, execution.AbandonResult{Run: execution.View{ID: id}, ErrorCode: cleanErrorCode(err)})
            if first == nil {
                first = err
            }
            continue
        }
        item, err := c.Abandoner.Preview(ctx, ref, in.Policy.Batch, in.PrepareRunner)
        if err != nil {
            item.Eligible = false
            item.ErrorCode = cleanErrorCode(err)
            if first == nil {
                first = err
            }
        }
        batch.Runs = append(batch.Runs, item)
    }
    return batch, first
}

// ExecuteAbandon revalidates prepared IDs after the caller obtains confirmation.
func (c *Projects) ExecuteAbandon(ctx context.Context, batch AbandonBatch, policy CallPolicy) (AbandonBatch, error) {
    if len(batch.Runs) == 0 || len(batch.Runs) > 1000 {
        return batch, fault.New("BAD_CONFIG", "argument_count", map[string]any{"Command": "loaf abandon"})
    }
    for _, item := range batch.Runs {
        if !item.Eligible || item.ErrorCode != "" {
            return batch, fault.New("ABANDON_NOT_PREPARED", "abandon_not_prepared", nil)
        }
    }
    if c.Abandoner == nil {
        return batch, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    batch.DryRun = false
    batch.Runs = append([]execution.AbandonResult(nil), batch.Runs...)
    var first error
    for i, item := range batch.Runs {
        if ctx.Err() != nil {
            item.ErrorCode = "ABANDON_NOT_ATTEMPTED"
            batch.Runs[i] = item
            if first == nil {
                first = fault.Wrap("ABANDON_CANCELED", "abandon_canceled", ctx.Err())
            }
            continue
        }
        done, err := c.Abandoner.Abandon(ctx, item, policy.Batch)
        if err != nil {
            done.ErrorCode = cleanErrorCode(err)
            if first == nil || done.ErrorCode == "ABANDON_UNCONFIRMED" {
                first = err
            }
        }
        batch.Runs[i] = done
    }
    return batch, first
}
