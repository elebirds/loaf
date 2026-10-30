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

// CleanupIndex changes only one saved locator's availability metadata.
type CleanupIndex interface {
    RunIndex
    RecordCleanup(context.Context, task.RunRef, *task.RunCleanup) error
}

// CleanupRemote is the current-helper boundary, independent of live supervisors.
type CleanupRemote interface {
    RunnerArch(context.Context, config.Target, bool) (string, error)
    Clean(context.Context, config.Target, string, wire.Request, bool) (*wire.CleanupReceipt, error)
}

// CleanupService composes explicit deletion without a persistent cleanup plan.
type CleanupService struct {
    Runs   *Service
    Index  CleanupIndex
    Remote CleanupRemote
    Runner func(context.Context, config.Target, string, bool) (string, error)
}

// CleanResult names the entire destructive scope. Eligible is only a preview;
// authoritative locks and ownership checks are reacquired during deletion.
type CleanResult struct {
    Run       View        `json:"run"`
    Path      string      `json:"path"`
    Eligible  bool        `json:"eligible"`
    Cleaned   bool        `json:"cleaned"`
    ErrorCode string      `json:"error_code,omitempty"`
    Ref       task.RunRef `json:"-"`
}

func cleanResult(ref task.RunRef) CleanResult {
    return CleanResult{Run: view(ref, nil), Path: path.Join(ref.RemoteDir, "runs", ref.ID), Cleaned: ref.Cleanup != nil && ref.Cleanup.CleanedAt != nil, Ref: ref}
}

// Preview reads a terminal outcome without granting deletion permission. Repair
// is explicit for real cleanup; dry-run never deploys or changes local state.
func (s CleanupService) Preview(ctx context.Context, ref task.RunRef, batch, repair bool) (CleanResult, error) {
    result := cleanResult(ref)
    if err := currentVersion(ref); err != nil {
        return result, err
    }
    if ref.Cleanup != nil {
        result.Eligible = true
        return result, nil
    }
    if ref.Submission != "submitted" {
        return result, cleanupFault("CLEAN_NOT_CONFIRMED", nil)
    }
    runner := ref.Runner
    if repair {
        prepared, err := s.Runs.prepareRef(ctx, ref, batch)
        if err != nil {
            return result, err
        }
        runner = prepared.Runner
    }
    timeout, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    req := request(ref, 0, 0)
    req.Action = "status"
    record, err := s.Runs.Remote.Control(timeout, ref.Connection, runner, req, batch)
    if err != nil {
        return result, cleanupFault(errorCode(err), err)
    }
    result.Run = view(ref, record)
    if !matching(ref, record) {
        return result, cleanupFault("BAD_FORMAT", nil)
    }
    if !wire.Summary(record).Valid() {
        return result, cleanupFault("CLEAN_NOT_TERMINAL", nil)
    }
    result.Eligible = true
    return result, nil
}

// Clean accepts only an explicit already-selected locator. Cancellation and lost
// confirmation retain the same locator and terminal summary for a safe retry.
func (s CleanupService) Clean(ctx context.Context, preview CleanResult, batch bool) (CleanResult, error) {
    ref := preview.Ref
    result := cleanResult(ref)
    if err := currentVersion(ref); err != nil {
        return result, err
    }
    if ref.Cleanup != nil && ref.Cleanup.CleanedAt != nil {
        result.Eligible = true
        return result, nil
    }
    if !preview.Eligible || ref.Submission != "submitted" || !summaryView(preview.Run).Valid() {
        return result, cleanupFault("CLEAN_NOT_CONFIRMED", nil)
    }
    setup, cancel := context.WithTimeout(ctx, 30*time.Second)
    arch, err := s.Remote.RunnerArch(setup, ref.Connection, batch)
    var runner string
    if err == nil {
        runner, err = s.Runner(setup, ref.Connection, arch, batch)
    }
    cancel()
    if err != nil {
        return preview, cleanupFault(errorCode(err), err)
    }
    if ctx.Err() != nil {
        return preview, cleanupFault("CLEAN_CANCELED", ctx.Err())
    }
    hadPending := ref.Cleanup != nil
    cleanup := ref.Cleanup
    if cleanup == nil {
        cleanup = &task.RunCleanup{RequestedAt: time.Now().UTC(), Summary: summaryView(preview.Run)}
    }
    if err := s.Index.RecordCleanup(ctx, ref, cleanup); err != nil {
        return preview, err
    }
    ref.Cleanup = cleanup
    result = cleanResult(ref)
    result.Eligible = true
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID}
    receipt, err := s.Remote.Clean(ctx, ref.Connection, runner, req, batch)
    if receipt != nil && !validCleanup(ref, receipt) {
        return result, cleanupFault("CLEAN_UNCONFIRMED", fault.New("BAD_RESPONSE", "clean_failed", nil))
    }
    if !hadPending && receipt == nil && err != nil && definiteCleanRefusal(errorCode(err)) {
        // No deletion was accepted. Clearing compares this attempt's timestamp
        // and cannot erase a concurrent successful cleanup.
        saved, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
        saveErr := s.Index.RecordCleanup(saved, ref, nil)
        cancel()
        result = preview
        if saveErr != nil {
            return result, saveErr
        }
        return result, cleanupFault(errorCode(err), err)
    }
    if receipt == nil || receipt.CleanedAt == nil || err != nil {
        return result, cleanupFault("CLEAN_UNCONFIRMED", err)
    }
    cleanup = &task.RunCleanup{RequestedAt: cleanup.RequestedAt, CleanedAt: receipt.CleanedAt, Summary: receipt.Summary}
    saved, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
    saveErr := s.Index.RecordCleanup(saved, ref, cleanup)
    cancelSave()
    if saveErr != nil {
        return result, cleanupFault("CLEAN_UNCONFIRMED", saveErr)
    }
    ref.Cleanup = cleanup
    result = cleanResult(ref)
    result.Eligible = true
    return result, nil
}

func summaryView(v View) wire.RunSummary {
    return wire.RunSummary{Status: v.Status, Phase: v.Phase, CreatedAt: v.CreatedAt, CompletedAt: v.CompletedAt, ExitCode: v.ExitCode, Signal: v.Signal, Reason: v.Reason, StopGraceMillis: v.StopGraceMillis}
}

func validCleanup(ref task.RunRef, receipt *wire.CleanupReceipt) bool {
    return receipt.FormatVersion == 1 && receipt.Version == wire.Version && receipt.Root == ref.RemoteDir && receipt.ProjectID == ref.ProjectID && receipt.RunID == ref.ID && receipt.Summary.Valid() && (receipt.CleanedAt == nil || !receipt.CleanedAt.IsZero())
}

func definiteCleanRefusal(code string) bool {
    switch code {
    case "CLEAN_NOT_TERMINAL", "CLEAN_NOT_CONFIRMED", "CLEAN_BUSY", "CLEAN_UNSAFE", "UNSAFE_PATH", "PROJECT_MISMATCH", "RUN_NOT_FOUND", "BAD_FORMAT", "VERSION_MISMATCH", "BAD_REQUEST":
        return true
    }
    return false
}

func cleanupFault(code string, cause error) *fault.Error {
    if code == "VERSION_MISMATCH" {
        var original *fault.Error
        if errors.As(cause, &original) && original.Code == code {
            return original
        }
        return versionMismatch("unknown")
    }
    message := "clean_failed"
    switch code {
    case "CLEAN_NOT_TERMINAL":
        message = "clean_not_terminal"
    case "CLEAN_NOT_CONFIRMED":
        message = "clean_not_confirmed"
    case "CLEAN_BUSY":
        message = "clean_busy"
    case "CLEAN_UNSAFE", "UNSAFE_PATH", "PROJECT_MISMATCH", "BAD_FORMAT":
        message = "clean_unsafe"
    case "CLEAN_UNCONFIRMED":
        message = "clean_unconfirmed"
    case "RUN_CLEANED":
        message = "clean_run_cleaned"
    case "CLEAN_CANCELED":
        message = "clean_canceled"
    }
    f := fault.Wrap(code, message, cause)
    f.Hint = "clean_retry"
    if code == "CLEAN_NOT_CONFIRMED" {
        f.Hint = "clean_confirm_submission"
    }
    if code == "RUN_CLEANED" || code == "CLEAN_CANCELED" {
        f.Hint = ""
    }
    var original *fault.Error
    if errors.As(cause, &original) {
        if detail, ok := original.Params["Detail"].(string); ok {
            f.Params = map[string]any{"Detail": detail}
        }
    }
    return f
}

// AvailabilityError rejects a locator whose cleanup metadata says its logs and
// outputs are removed or not confirmed available.
func AvailabilityError(ref task.RunRef) error {
    if err := currentVersion(ref); err != nil {
        return err
    }
    if ref.Abandonment != nil && ref.Abandonment.NoStartAt != nil {
        return abandonFault("RUN_ABANDONED", nil)
    }
    if ref.Cleanup == nil {
        return nil
    }
    if ref.Cleanup.CleanedAt != nil {
        return cleanupFault("RUN_CLEANED", nil)
    }
    return cleanupFault("CLEAN_UNCONFIRMED", nil)
}
