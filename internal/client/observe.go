package client

import (
    "context"
    "errors"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/wire"
)

// PSInput selects either one run or a bounded project history.
type PSInput struct {
    ID                         string
    Local                      bool
    Wait                       string
    Limit                      *int
    Target, Name, Since, Until *string
    Statuses, Tags             []string
    Policy                     CallPolicy
}

// PSResult preserves distinct single-run, offline and list contracts.
type PSResult struct {
    View    *execution.View
    Details *execution.LocalDetails
    History *execution.History
}

// PS observes saved bindings, never redirects an existing run to a new target.
func (c *Projects) PS(ctx context.Context, in PSInput) (PSResult, error) {
    budget, err := config.ResolveWait(in.Wait)
    if err != nil {
        return PSResult{}, err
    }
    if (in.ID == "" || in.Local) && budget > 0 {
        return PSResult{}, fault.New("BAD_CONFIG", "invalid_flags", nil)
    }
    if in.ID != "" && (in.Target != nil || in.Name != nil || in.Since != nil || in.Until != nil || in.Limit != nil || in.Statuses != nil || in.Tags != nil) {
        return PSResult{}, fault.New("BAD_CONFIG", "ps_list_only", nil)
    }
    service := c.Runs
    if in.Local {
        service = &execution.Service{Index: c.State, Metadata: c.State, Observations: c.State}
    }
    if service == nil {
        return PSResult{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, file, err := c.boundProject(ctx)
    if err != nil {
        return PSResult{}, err
    }
    if in.ID != "" {
        if in.Local {
            detail, err := service.Inspect(project, in.ID)
            if err != nil {
                return PSResult{}, err
            }
            return PSResult{Details: &detail}, nil
        }
        ref, err := service.Select(project.ID, in.ID)
        if err != nil {
            return PSResult{}, err
        }
        tail := 0
        if budget > 0 {
            tail, err = config.ResolveTail(c.Settings.Logs, file.Logs)
            if err != nil {
                return PSResult{}, err
            }
        }
        observed, err := service.Observe(ctx, ref, budget, tail, in.Policy.Batch)
        if budget == 0 {
            observed.Log = nil
        }
        return PSResult{View: &observed}, err
    }
    layers := []config.PSSettings{c.Settings.PS, file.PS}
    if in.Limit != nil {
        layers = append(layers, config.PSSettings{Limit: in.Limit})
    }
    limit, err := config.ResolvePSLimit(layers...)
    if err != nil {
        return PSResult{}, err
    }
    filters := execution.HistoryFilters{Statuses: in.Statuses, Target: value(in.Target), Name: value(in.Name), Tags: in.Tags}
    for _, bound := range []struct {
        input *string
        dest  **time.Time
    }{{in.Since, &filters.Since}, {in.Until, &filters.Until}} {
        if bound.input == nil {
            continue
        }
        parsed, err := time.Parse(time.RFC3339Nano, *bound.input)
        if err != nil {
            return PSResult{}, fault.New("BAD_CONFIG", "ps_invalid_time", nil)
        }
        parsed = parsed.UTC()
        *bound.dest = &parsed
    }
    if in.Target != nil && !config.ValidName(*in.Target) {
        return PSResult{}, fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    var history execution.History
    if in.Local {
        history, err = service.LocalHistory(ctx, project.ID, filters, limit)
    } else {
        history, err = service.History(ctx, project.ID, filters, limit, in.Policy.Batch)
    }
    return PSResult{History: &history}, err
}

// Inspect reads only the current project's saved public facts.
func (c *Projects) Inspect(ctx context.Context, id string) (execution.LocalDetails, error) {
    project, _, err := c.boundProject(ctx)
    if err != nil {
        return execution.LocalDetails{}, err
    }
    service := execution.Service{Index: c.State, Metadata: c.State, Observations: c.State}
    return service.Inspect(project, id)
}

// StopInput retains the distinction between default grace and explicit zero.
type StopInput struct {
    ID     string
    Grace  *string
    Policy CallPolicy
}

// Stop sends one request and observes the same bound run.
func (c *Projects) Stop(ctx context.Context, in StopInput, emit ProgressFunc) (execution.View, error) {
    if c.Runs == nil {
        return execution.View{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, file, err := c.boundProject(ctx)
    if err != nil {
        return execution.View{}, err
    }
    layers := []config.StopSettings{c.Settings.Stop, file.Stop}
    if in.Grace != nil {
        if *in.Grace == "" {
            return execution.View{}, fault.New("BAD_CONFIG", "invalid_stop_grace", nil)
        }
        layers = append(layers, config.StopSettings{Grace: *in.Grace})
    }
    grace, err := config.ResolveStopGrace(layers...)
    if err != nil {
        return execution.View{}, err
    }
    ref, err := c.Runs.Select(project.ID, in.ID)
    if err != nil {
        return execution.View{}, err
    }
    progress(emit, Progress{Stage: "stop"})
    return c.Runs.Stop(ctx, ref, grace, in.Policy.Batch)
}

// Wait observes a frozen set without scheduling or stopping workloads.
func (c *Projects) Wait(ctx context.Context, ids, tags []string, timeout time.Duration, policy CallPolicy, changed func(execution.View)) (execution.WaitResult, error) {
    if timeout <= 0 || timeout > 24*time.Hour {
        return execution.WaitResult{}, fault.New("BAD_CONFIG", "wait_timeout", nil)
    }
    if c.Runs == nil {
        return execution.WaitResult{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, _, err := c.boundProject(ctx)
    if err != nil {
        return execution.WaitResult{}, err
    }
    refs, err := c.Runs.Freeze(project.ID, ids, tags)
    if err != nil {
        return execution.WaitResult{}, err
    }
    return c.Runs.Wait(ctx, refs, timeout, policy.Batch, changed)
}

// LogsInput describes bounded observation or explicitly opted-in cursor recovery.
type LogsInput struct {
    ID                            string
    Tail                          *int
    Follow, Reconnect             bool
    Resume, ResumeFile, AckCursor *string
    RecoveryBudget                *time.Duration
    Policy                        CallPolicy
}

// LogsResult includes offline cursor acknowledgement or an observed run.
type LogsResult struct {
    View         execution.View
    Acknowledged bool
    Offset       int64
}

// LogsFeedback reports consumed chunks and disconnected waiting, not output format.
// Emit returns true only when successful consumption may advance the resume file.
// An error stops observation without acknowledging that chunk.
type LogsFeedback struct {
    Emit     func(execution.View, string) (acknowledged bool, err error)
    Recovery func(string)
}

// Logs persists only explicitly acknowledged chunks after successful consumption.
func (c *Projects) Logs(ctx context.Context, in LogsInput, feedback LogsFeedback) (LogsResult, error) {
    fileName, ack, resume := value(in.ResumeFile), value(in.AckCursor), value(in.Resume)
    budget := value(in.RecoveryBudget)
    if (in.ResumeFile != nil && fileName == "") || (in.AckCursor != nil && ack == "") || (fileName != "" && in.Resume != nil) ||
        (ack != "" && (fileName == "" || in.Follow || in.Reconnect || in.Tail != nil || in.RecoveryBudget != nil)) ||
        (fileName != "" && ack == "" && !in.Follow) || budget < 0 || budget > 24*time.Hour || (in.RecoveryBudget != nil && !in.Follow) ||
        ((in.Resume != nil || in.Reconnect) && !in.Follow) || (in.Resume != nil && (resume == "" || in.Tail != nil)) {
        return LogsResult{}, fault.New("BAD_CONFIG", "log_resume_flags", nil)
    }
    service := c.Runs
    if ack != "" {
        service = &execution.Service{Index: c.State, Metadata: c.State}
    }
    if service == nil {
        return LogsResult{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, file, err := c.boundProject(ctx)
    if err != nil {
        return LogsResult{}, err
    }
    layers := []config.LogSettings{c.Settings.Logs, file.Logs}
    if in.Tail != nil {
        layers = append(layers, config.LogSettings{Tail: in.Tail})
    }
    tail, err := config.ResolveTail(layers...)
    if err != nil {
        return LogsResult{}, err
    }
    ref, err := service.Select(project.ID, in.ID)
    if err != nil {
        return LogsResult{}, err
    }
    if ack != "" {
        cursor, err := wire.DecodeLogCursor(ack)
        if err != nil || !cursor.Matches(wire.Request{Version: ref.Version, Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID}) {
            return LogsResult{}, fault.New("BAD_CONFIG", "log_cursor_invalid", nil)
        }
        result := LogsResult{View: execution.View{ID: ref.ID}}
        if err := local.SaveLogCheckpoint(ctx, fileName, ack); err != nil {
            result.View.StateError, err = checkpointError(err)
            return result, err
        }
        result.Acknowledged, result.Offset = true, cursor.Offset
        return result, nil
    }
    if fileName != "" {
        resume, err = local.ReadLogCheckpoint(fileName)
        if err != nil {
            return LogsResult{}, err
        }
        if resume != "" && in.Tail != nil {
            return LogsResult{}, fault.New("BAD_CONFIG", "log_resume_flags", nil)
        }
    }
    if err := execution.AvailabilityError(ref); err != nil {
        return LogsResult{}, err
    }
    if !in.Follow {
        observed, err := service.Observe(ctx, ref, 0, tail, in.Policy.Batch)
        return LogsResult{View: observed}, err
    }
    var checkpointErr error
    var checkpointCode string
    emit := func(view execution.View, kind string) error {
        if feedback.Emit == nil {
            return nil
        }
        acknowledged, err := feedback.Emit(view, kind)
        if err != nil {
            return err
        }
        if acknowledged && fileName != "" && view.Log != nil {
            if err := local.SaveLogCheckpoint(ctx, fileName, view.Log.Cursor); err != nil {
                checkpointCode, checkpointErr = checkpointError(err)
                return checkpointErr
            }
        }
        return nil
    }
    var observed execution.View
    if in.Reconnect || resume != "" || fileName != "" || in.RecoveryBudget != nil {
        observed, err = service.FollowRecovering(ctx, ref, 0, tail, in.Policy.Batch, resume, execution.RecoveryOptions{Budget: budget, Progress: feedback.Recovery}, emit)
    } else {
        observed, err = service.Follow(ctx, ref, 0, tail, in.Policy.Batch, emit)
    }
    if checkpointErr != nil {
        observed.StateError = checkpointCode
    }
    return LogsResult{View: observed}, err
}

// checkpointError preserves validation/safety faults and gives native I/O
// failures a checkpoint-specific code rather than a remote query failure.
func checkpointError(err error) (string, error) {
    var known *fault.Error
    if errors.As(err, &known) {
        return known.Code, err
    }
    if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
        return "OPERATION_CANCELED", fault.Wrap("OPERATION_CANCELED", "operation_canceled", err)
    }
    return "LOG_CHECKPOINT_FAILED", fault.Wrap("LOG_CHECKPOINT_FAILED", "log_checkpoint_failed", err)
}
