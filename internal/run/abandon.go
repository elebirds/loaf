package run

import (
    "context"
    "errors"
    "path"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

// AbandonIndex retains no-start metadata independently of execution outcomes.
type AbandonIndex interface {
    RunIndex
    RecordAbandonment(context.Context, task.RunRef, *task.RunAbandonment) error
}

// AbandonRemote fences and reclaims a preparation through the current helper.
type AbandonRemote interface {
    Abandon(context.Context, config.Target, string, wire.Request, bool, bool) (*wire.AbandonReceipt, error)
}

// AbandonService owns the explicit no-start and payload-reclamation workflow.
type AbandonService struct {
    Runs   *Service
    Index  AbandonIndex
    Remote AbandonRemote
}

// AbandonResult preserves confirmed no-start and deletion independently, even
// when transport or local persistence fails after the remote state changes.
type AbandonResult struct {
    Run        View        `json:"run"`
    Path       string      `json:"path"`
    Eligible   bool        `json:"eligible"`
    NoStartAt  *time.Time  `json:"no_start_at,omitempty"`
    CleanedAt  *time.Time  `json:"cleaned_at,omitempty"`
    ErrorCode  string      `json:"error_code,omitempty"`
    StateError string      `json:"state_error,omitempty"`
    Ref        task.RunRef `json:"-"`
}

func abandonResult(ref task.RunRef) AbandonResult {
    r := AbandonResult{Run: view(ref, nil), Path: path.Join(ref.RemoteDir, "runs", ref.ID), Ref: ref}
    if ref.Abandonment != nil {
        r.NoStartAt, r.CleanedAt = ref.Abandonment.NoStartAt, ref.Abandonment.CleanedAt
    }
    return r
}

func abandonEligible(ref task.RunRef) error {
    if err := currentVersion(ref); err != nil {
        return err
    }
    if ref.QueueRequestID != "" {
        return abandonFault("ABANDON_USE_QUEUE", nil)
    }
    if ref.Submission == "submitted" || ref.Cleanup != nil {
        return abandonFault("ABANDON_NOT_PREPARED", nil)
    }
    return nil
}

// Preview observes one frozen locator; helper preparation requires explicit
// repair permission and dry-run callers never grant it.
func (s AbandonService) Preview(ctx context.Context, ref task.RunRef, batch, repair bool) (AbandonResult, error) {
    result := abandonResult(ref)
    if err := abandonEligible(ref); err != nil {
        return result, err
    }
    if result.CleanedAt != nil {
        result.Eligible = true
        return result, nil
    }
    runner := ref.Runner
    if repair {
        prepared, err := s.Runs.prepareRef(ctx, ref, batch)
        if err != nil {
            return result, err
        }
        runner = prepared.Runner
    }
    limited, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    receipt, err := s.Remote.Abandon(limited, ref.Connection, runner, request(ref, 0, 0), true, batch)
    if receipt != nil {
        if !validAbandon(ref, receipt) {
            return result, abandonFault("BAD_RESPONSE", nil)
        }
        result.NoStartAt, result.CleanedAt = receipt.NoStartAt, receipt.CleanedAt
    }
    if err != nil {
        return result, abandonFault(errorCode(err), err)
    }
    if receipt == nil {
        return result, abandonFault("BAD_RESPONSE", nil)
    }
    result.Eligible = true
    return result, nil
}

// Abandon never infers no-start from the local submission value. The remote
// owner revalidates admission and lifecycle locks for this exact ID.
func (s AbandonService) Abandon(ctx context.Context, preview AbandonResult, batch bool) (AbandonResult, error) {
    ref, err := s.Index.Run(preview.Ref.ProjectID, preview.Ref.ID)
    if err != nil {
        return preview, err
    }
    result := abandonResult(ref)
    if err := abandonEligible(ref); err != nil {
        return result, err
    }
    if !preview.Eligible {
        return result, abandonFault("ABANDON_NOT_PREPARED", nil)
    }
    result.Eligible = true
    if result.CleanedAt != nil {
        return result, nil
    }
    prepared, err := s.Runs.prepareRef(ctx, ref, batch)
    if err != nil {
        return result, err
    }
    if ctx.Err() != nil {
        return result, abandonFault("ABANDON_CANCELED", ctx.Err())
    }
    hadPending := ref.Abandonment != nil
    pending := ref.Abandonment
    if pending == nil {
        pending = &task.RunAbandonment{RequestedAt: time.Now().UTC()}
    }
    if err := s.Index.RecordAbandonment(ctx, ref, pending); err != nil {
        result.StateError = errorCode(err)
        return result, err
    }
    ref.Abandonment = pending
    result = abandonResult(ref)
    result.Eligible = true
    receipt, remoteErr := s.Remote.Abandon(ctx, ref.Connection, prepared.Runner, request(ref, 0, 0), false, batch)
    if receipt != nil && !validAbandon(ref, receipt) {
        return result, abandonFault("ABANDON_UNCONFIRMED", fault.New("BAD_RESPONSE", "abandon_failed", nil))
    }
    if receipt != nil && receipt.NoStartAt != nil {
        confirmed := &task.RunAbandonment{RequestedAt: pending.RequestedAt, NoStartAt: receipt.NoStartAt, CleanedAt: receipt.CleanedAt}
        ref.Abandonment = confirmed
        result = abandonResult(ref)
        result.Eligible = true
        saved, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
        saveErr := s.Index.RecordAbandonment(saved, ref, confirmed)
        cancel()
        if saveErr != nil {
            result.StateError = errorCode(saveErr)
            if remoteErr != nil || receipt.CleanedAt == nil {
                return result, errors.Join(abandonFault("ABANDON_UNCONFIRMED", remoteErr), saveErr)
            }
            return result, saveErr
        }
        if remoteErr != nil || receipt.CleanedAt == nil {
            return result, abandonFault("ABANDON_UNCONFIRMED", remoteErr)
        }
        return result, nil
    }
    if remoteErr != nil && definiteAbandonRefusal(errorCode(remoteErr)) {
        if !hadPending {
            saved, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
            saveErr := s.Index.RecordAbandonment(saved, ref, nil)
            cancel()
            if saveErr != nil {
                result.StateError = errorCode(saveErr)
                return result, errors.Join(abandonFault(errorCode(remoteErr), remoteErr), saveErr)
            }
            current, loadErr := s.Index.Run(ref.ProjectID, ref.ID)
            if loadErr != nil {
                result.StateError = errorCode(loadErr)
                return result, loadErr
            }
            result = abandonResult(current)
        }
        return result, abandonFault(errorCode(remoteErr), remoteErr)
    }
    return result, abandonFault("ABANDON_UNCONFIRMED", remoteErr)
}

func validAbandon(ref task.RunRef, r *wire.AbandonReceipt) bool {
    return r.Valid() && r.Version == wire.Version && r.Root == ref.RemoteDir && r.ProjectID == ref.ProjectID && r.RunID == ref.ID
}

func definiteAbandonRefusal(code string) bool {
    switch code {
    case "ABANDON_NOT_PREPARED", "ABANDON_USE_QUEUE", "ABANDON_BUSY", "ABANDON_UNSAFE", "ABANDON_CANCELED", "BAD_FORMAT", "VERSION_MISMATCH", "BAD_REQUEST", "UNSAFE_PATH", "PROJECT_MISMATCH", "PROJECT_UNOWNED":
        return true
    }
    return false
}

func abandonFault(code string, cause error) *fault.Error {
    if code == "VERSION_MISMATCH" {
        var f *fault.Error
        if errors.As(cause, &f) && f.Code == code {
            return f
        }
        return versionMismatch("unknown")
    }
    message := "abandon_failed"
    switch code {
    case "ABANDON_NOT_PREPARED":
        message = "abandon_not_prepared"
    case "ABANDON_USE_QUEUE":
        message = "abandon_use_queue"
    case "ABANDON_BUSY":
        message = "abandon_busy"
    case "ABANDON_UNSAFE", "BAD_FORMAT", "BAD_REQUEST", "UNSAFE_PATH", "PROJECT_MISMATCH", "PROJECT_UNOWNED":
        message = "abandon_unsafe"
    case "ABANDON_UNCONFIRMED":
        message = "abandon_unconfirmed"
    case "ABANDON_CANCELED":
        message = "abandon_canceled"
    case "RUN_ABANDONED":
        message = "run_abandoned"
    }
    f := fault.Wrap(code, message, cause)
    if code == "ABANDON_UNCONFIRMED" {
        f.Hint = "abandon_retry"
    }
    return f
}
