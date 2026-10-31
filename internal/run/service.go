package run

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

// RunIndex is the concrete local storage seam consumed by execution.
type RunIndex interface {
    SaveRun(context.Context, task.RunRef) error
    Run(string, string) (task.RunRef, error)
    Runs(string) ([]task.RunRef, error)
}

// MetadataStore is optional for existing execution callers, required only for
// annotations. Storage remains local and separate from execution records.
type MetadataStore interface {
    RunMetadata(string, string) (task.RunMetadata, error)
    SaveRunMetadata(context.Context, string, string, task.RunMetadata) error
}

// Remote is the SSH control and observation boundary, independent of rendering.
type Remote interface {
    Control(context.Context, config.Target, string, wire.Request, bool) (*wire.RunRecord, error)
    Observe(context.Context, config.Target, string, wire.Request, bool) (wire.Observation, error)
    Follow(context.Context, config.Target, string, wire.Request, bool, func(wire.LogEvent) error) error
}

// Service composes existing SSH, native source transfer and private run locators.
type Service struct {
    Index        RunIndex
    Check        sources.Checker
    Runner       func(context.Context, config.Target, string, bool) (string, error)
    Send         sources.Sender
    Remote       Remote
    Metadata     MetadataStore
    Observations ObservationStore
}

// SubmitInput freezes one project's explicit execution inputs.
type SubmitInput struct {
    // BeforeSync records optional waiting relationships after identity is saved
    // and before any project transfer or execution side effect.
    BeforeSync func(context.Context, task.RunRef) error
    Project    config.Project
    File       config.ProjectFile
    Target     string
    Connection config.Target
    RemoteDir  string
    Plan       sources.Plan
    Args       []string
    Batch      bool
    Metadata   task.RunMetadata
}

// View exposes operational results without leaking private request environments.
type View struct {
    ID               string               `json:"run_id"`
    Version          string               `json:"version"`
    Name             string               `json:"name,omitempty"`
    Tags             []string             `json:"tags,omitempty"`
    Target           string               `json:"target"`
    RemoteDir        string               `json:"remote_dir"`
    Status           string               `json:"status"`
    Submission       string               `json:"submission,omitempty"`
    Phase            string               `json:"phase,omitempty"`
    CreatedAt        time.Time            `json:"created_at"`
    CompletedAt      *time.Time           `json:"completed_at,omitempty"`
    ExitCode         *int                 `json:"exit_code"`
    Signal           string               `json:"signal,omitempty"`
    Reason           string               `json:"reason,omitempty"`
    QueryError       string               `json:"query_error,omitempty"`
    StateError       string               `json:"state_error,omitempty"`
    Log              *wire.LogChunk       `json:"log,omitempty"`
    StopGraceMillis  *int64               `json:"stop_grace_millis,omitempty"`
    CleanedAt        *time.Time           `json:"cleaned_at,omitempty"`
    CleanupPending   bool                 `json:"cleanup_pending,omitempty"`
    Abandonment      *task.RunAbandonment `json:"abandonment,omitempty"`
    LastObservation  *task.ObservedRun    `json:"last_observation,omitempty"`
    LastQueryFailure *task.QueryFailure   `json:"last_query_failure,omitempty"`
    ObservedAt       time.Time            `json:"observed_at,omitzero"`
    QueryInterrupted bool                 `json:"query_interrupted,omitempty"`
    Ref              task.RunRef          `json:"-"`
}

func view(ref task.RunRef, r *wire.RunRecord) View {
    v := View{ID: ref.ID, Version: ref.Version, Name: ref.Name, Tags: ref.Tags, Target: ref.Target, RemoteDir: ref.RemoteDir, CreatedAt: ref.CreatedAt, Status: "unknown", Submission: ref.Submission, Abandonment: ref.Abandonment, Ref: ref}
    if ref.Cleanup != nil {
        summary := ref.Cleanup.Summary
        v.Status, v.Phase, v.CreatedAt = summary.Status, summary.Phase, summary.CreatedAt
        v.CompletedAt, v.ExitCode = summary.CompletedAt, summary.ExitCode
        v.Signal, v.Reason, v.StopGraceMillis = summary.Signal, summary.Reason, summary.StopGraceMillis
        v.CleanedAt = ref.Cleanup.CleanedAt
        v.CleanupPending = v.CleanedAt == nil
        return v
    }
    if r != nil {
        v.Status = r.Status
        v.Phase = r.Phase
        v.CreatedAt = r.CreatedAt
        v.CompletedAt = r.CompletedAt
        v.ExitCode = r.ExitCode
        v.Signal = r.Signal
        v.Reason = r.Reason
        v.StopGraceMillis = r.StopGraceMillis
        if r.SupervisorPID == 0 && !wire.Terminal(r.Status) {
            v.Status = "unknown"
            v.Reason = "SUBMISSION_UNCONFIRMED"
        }
    }
    return v
}

// Submit never retries execution. Lost confirmation is resolved against the
// same already-persisted ID, not by replaying a start request.
func (s Service) Submit(ctx context.Context, in SubmitInput, progress func(string)) (View, error) {
    metadata, err := task.NormalizeRunMetadata(in.Metadata)
    if err != nil {
        return View{}, err
    }
    if (metadata.Name != "" || len(metadata.Tags) > 0) && s.Metadata == nil {
        return View{}, fault.New("BAD_CONFIG", "run_metadata_unavailable", nil)
    }
    spec, err := ResolveExecution(in.File, in.Args)
    if err != nil {
        return View{}, err
    }
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "launch", ProjectID: in.Project.ID, Args: spec.Args, Setup: spec.Setup, Env: spec.Env}
    notify := func(stage string) {
        if progress != nil {
            progress(stage)
        }
    }
    notify("checking")
    preflight, cancel := context.WithTimeout(ctx, 30*time.Second)
    report, err := s.Check.Check(preflight, in.Connection, in.RemoteDir, in.Batch)
    if err != nil {
        cancel()
        return View{}, err
    }
    runner, err := s.Runner(preflight, in.Connection, report.Arch, in.Batch)
    cancel()
    if err != nil {
        return View{}, err
    }
    var id [12]byte
    if _, err := rand.Read(id[:]); err != nil {
        return View{}, err
    }
    ref := task.RunRef{ID: "r_" + hex.EncodeToString(id[:]), ProjectID: in.Project.ID, Target: in.Target, Connection: in.Connection, RemoteDir: report.RemoteDir, Runner: runner, Version: wire.Version, Outputs: spec.Outputs, CreatedAt: time.Now().UTC(), Submission: "preparing"}
    ref.Name, ref.Tags = metadata.Name, metadata.Tags
    if err := s.Index.SaveRun(ctx, ref); err != nil {
        return View{}, err
    }
    if metadata.Name != "" || len(metadata.Tags) > 0 {
        if err := s.Metadata.SaveRunMetadata(ctx, ref.ProjectID, ref.ID, metadata); err != nil {
            result := view(ref, nil)
            err = recordStateError(&result, err)
            return result, err
        }
    }
    if in.BeforeSync != nil {
        if err := in.BeforeSync(ctx, ref); err != nil {
            result := view(ref, nil)
            result.StateError = "NOTIFY_FAILED"
            return result, err
        }
    }
    input := ssh.SyncInput{Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, Mode: in.Plan.Policy.Mode}
    for _, file := range in.Plan.Files {
        input.Paths = append(input.Paths, file.Path)
    }
    notify("syncing")
    if _, err := s.Send.Sync(ctx, in.Connection, runner, in.Plan.Root, input, in.Batch); err != nil {
        var failure *fault.Error
        if errors.As(err, &failure) && failure.Hint == "sync_retry" {
            copy := *failure
            copy.Hint = "run_sync_retry"
            err = &copy
        }
        return view(ref, nil), err
    }
    now := time.Now().UTC()
    ref.AttemptedAt = &now
    ref.Submission = "uncertain"
    if err := s.Index.SaveRun(ctx, ref); err != nil {
        ref.Submission = "preparing"
        ref.AttemptedAt = nil
        result := view(ref, nil)
        err = recordStateError(&result, err)
        return result, err
    }
    req.RunID = ref.ID
    req.Root = ref.RemoteDir
    notify("submitting")
    submitCtx, stop := context.WithTimeout(ctx, 20*time.Second)
    record, err := s.Remote.Control(submitCtx, ref.Connection, ref.Runner, req, in.Batch)
    stop()
    if err != nil {
        var rejected *ssh.RemoteError
        if errors.As(err, &rejected) && definiteRejection(rejected.Code) {
            ref.Submission = "preparing"
            ref.AttemptedAt = nil
            saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
            saveErr := s.Index.SaveRun(saveCtx, ref)
            cancel()
            if saveErr != nil {
                result := view(ref, record)
                saveErr = recordStateError(&result, saveErr)
                return result, saveErr
            }
            return view(ref, record), remoteFault(err)
        }
        // The caller may have canceled after acceptance; recovery is a bounded,
        // read-only check and cannot restart or stop the remote process.
        recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
        query := req
        query.Action = "status"
        recovered, recoveryErr := s.Remote.Control(recovery, ref.Connection, ref.Runner, query, true)
        cancel()
        if recoveryErr == nil {
            record = recovered
        }
    }
    if !accepted(ref, record) {
        result := view(ref, record)
        f := fault.New("SUBMISSION_UNKNOWN", "submission_unknown", nil)
        f.Cause = err
        var original *fault.Error
        if errors.As(err, &original) {
            f.Params = original.Params
        }
        f.Hint = "submission_query"
        return result, fault.At(f, "launch")
    }
    ref.Submission = "submitted"
    saved, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
    saveErr := s.Index.SaveRun(saved, ref)
    cancelSave()
    result := view(ref, record)
    saveErr = recordStateError(&result, saveErr)
    return result, saveErr
}

func definiteRejection(code string) bool {
    switch code {
    case "BAD_COMMAND", "BAD_ENV", "BAD_FORMAT", "VERSION_MISMATCH", "UNSAFE_PATH", "PROJECT_MISMATCH", "RUN_NOT_FOUND", "COPY_FAILED", "START_FAILED", "RUN_ABANDONED":
        return true
    }
    return false
}

func matching(ref task.RunRef, r *wire.RunRecord) bool {
    if r == nil || r.FormatVersion != wire.FormatVersion || r.Request.Version != ref.Version || r.Request.RunID != ref.ID || r.Request.ProjectID != ref.ProjectID || r.Request.Root != ref.RemoteDir {
        return false
    }
    return ref.QueueRequestID == "" || ref.QueueRequestID == r.Request.QueueRequestID || r.Request.QueueRequestID == "" && r.SupervisorPID == 0 && !wire.Terminal(r.Status) && (r.Phase == "copying" || r.Phase == "prepared")
}
func accepted(ref task.RunRef, r *wire.RunRecord) bool {
    return matching(ref, r) && (ref.QueueRequestID == "" || ref.QueueRequestID == r.Request.QueueRequestID) && (r.SupervisorPID > 0 || wire.Terminal(r.Status) && r.Phase != "copying")
}

// Select resolves an ID only within the current project. Ambiguity never falls
// back to an older, successful run.
func (s Service) Select(projectID, id string) (task.RunRef, error) {
    if id != "" {
        ref, err := s.Index.Run(projectID, id)
        if err != nil {
            return task.RunRef{}, err
        }
        return s.describe(ref)
    }
    refs, err := s.Index.Runs(projectID)
    if err != nil {
        return task.RunRef{}, err
    }
    if len(refs) == 0 {
        return task.RunRef{}, fault.New("RUN_NOT_FOUND", "run_not_found", nil)
    }
    if len(refs) > 1 && refs[0].AttemptedAt.Equal(*refs[1].AttemptedAt) {
        return task.RunRef{}, fault.New("RUN_AMBIGUOUS", "run_ambiguous", nil)
    }
    return s.describe(refs[0])
}

func (s Service) describe(ref task.RunRef) (task.RunRef, error) {
    if s.Metadata != nil {
        m, err := s.Metadata.RunMetadata(ref.ProjectID, ref.ID)
        if err != nil {
            return ref, err
        }
        ref.Name, ref.Tags = m.Name, m.Tags
    }
    return ref, nil
}

func request(ref task.RunRef, wait time.Duration, tail int) wire.Request {
    return wire.Request{FormatVersion: wire.FormatVersion, Version: ref.Version, Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, WaitMillis: int64((wait + time.Millisecond - 1) / time.Millisecond), Tail: tail}
}

// Observe queries a bound locator without reading today's target configuration.
func (s Service) Observe(ctx context.Context, ref task.RunRef, wait time.Duration, tail int, batch bool) (result View, resultErr error) {
    var queryErr error
    defer func() {
        if err := s.remember(ctx, ref, result, queryErr); err != nil {
            resultErr = errors.Join(resultErr, recordStateError(&result, err))
        }
    }()
    if err := currentVersion(ref); err != nil {
        return view(ref, nil), err
    }
    if ref.Cleanup != nil || ref.Abandonment != nil && ref.Abandonment.NoStartAt != nil {
        return view(ref, nil), nil
    }
    bound := ref
    prepared, prepareErr := s.prepareRef(ctx, ref, batch)
    if prepareErr != nil {
        result := view(ref, nil)
        result.QueryError = errorCode(prepareErr)
        queryErr = prepareErr
        return result, prepareErr
    }
    ref = prepared
    timeout, cancel := context.WithTimeout(ctx, wait+20*time.Second)
    defer cancel()
    observed, err := s.Remote.Observe(timeout, ref.Connection, ref.Runner, request(ref, wait, tail), batch)
    result = view(ref, nil)
    if matching(ref, observed.Record) {
        result = view(ref, observed.Record)
    }
    if err != nil {
        if ctx.Err() != nil {
            return result, fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", ctx.Err())
        }
        result.QueryError = errorCode(err)
        queryErr = remoteFault(err)
        return result, queryErr
    }
    if observed.Record != nil && !wire.CompatibleVersion(wire.Version, observed.Record.Request.Version) {
        queryErr = versionMismatch(observed.Record.Request.Version)
        result.QueryError = errorCode(queryErr)
        return result, queryErr
    }
    if !matching(ref, observed.Record) {
        queryErr = fault.New("BAD_RESPONSE", "run_query", nil)
        result.QueryError = errorCode(queryErr)
        return result, queryErr
    }
    result.ObservedAt = time.Now().UTC()
    result.Log = &observed.Log
    queueAccepted := ref.QueueRequestID != "" && accepted(ref, observed.Record) && observed.Record.Request.AttemptedAt != nil && !observed.Record.Request.AttemptedAt.IsZero()
    if ref.Submission == "uncertain" && result.Status != "unknown" && accepted(ref, observed.Record) || queueAccepted && (ref.Submission == "preparing" || ref.Submission == "uncertain") {
        // Confirm the saved ID after a successful observation. This changes
        // only its local submission locator, never sends another start.
        bound.Submission = "submitted"
        if queueAccepted {
            attempted := observed.Record.Request.AttemptedAt.UTC()
            bound.AttemptedAt = &attempted
        }
        result.Ref = bound
        result.Submission = bound.Submission
        saved, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
        saveErr := s.Index.SaveRun(saved, bound)
        cancelSave()
        if saveErr != nil {
            saveErr = recordStateError(&result, saveErr)
            return result, saveErr
        }
    }
    return result, nil
}

// Follow observes one run over one SSH connection; callback errors only close it.
func (s Service) Follow(ctx context.Context, ref task.RunRef, wait time.Duration, tail int, batch bool, emit func(View, string) error) (View, error) {
    return s.follow(ctx, ref, wait, tail, batch, "", false, RecoveryOptions{}, emit)
}

// FollowResumable resumes only confirmed chunks and permits one transient
// reconnect. Existing Follow keeps its single-connection behavior.
func (s Service) FollowResumable(ctx context.Context, ref task.RunRef, wait time.Duration, tail int, batch bool, cursor string, emit func(View, string) error) (View, error) {
    return s.follow(ctx, ref, wait, tail, batch, cursor, true, RecoveryOptions{}, emit)
}

func (s Service) follow(ctx context.Context, ref task.RunRef, wait time.Duration, tail int, batch bool, cursor string, recover bool, recovery RecoveryOptions, emit func(View, string) error) (last View, resultErr error) {
    var queryErr error
    defer func() {
        if err := s.remember(ctx, ref, last, queryErr); err != nil {
            resultErr = errors.Join(resultErr, recordStateError(&last, err))
        }
    }()
    if err := AvailabilityError(ref); err != nil {
        return view(ref, nil), err
    }
    req := request(ref, wait, tail)
    confirmedOffset := int64(-1)
    confirmedFile := ""
    if cursor != "" {
        c, err := wire.DecodeLogCursor(cursor)
        if err != nil || !c.Matches(req) {
            return view(ref, nil), fault.New("BAD_CONFIG", "log_cursor_invalid", nil)
        }
        confirmedOffset, confirmedFile = c.Offset, c.FileID
    }
    bound := ref
    prepared, err := s.prepareRef(ctx, ref, batch)
    if err != nil {
        queryErr = err
        last = view(ref, nil)
        last.QueryError = errorCode(err)
        return last, err
    }
    ref = prepared
    last = view(ref, nil)
    deadline := time.Time{}
    if wait > 0 {
        deadline = time.Now().Add(wait)
    }
    recoveryDeadline := time.Time{}
    recovering := false
    remainingRecovery := recovery.Budget
    for attempt := 0; ; attempt++ {
        req.Resume = cursor
        if !deadline.IsZero() {
            remaining := time.Until(deadline)
            if remaining <= 0 && attempt > 0 {
                break
            }
            if remaining <= 0 {
                remaining = time.Millisecond
            }
            req.WaitMillis = int64((remaining + time.Millisecond - 1) / time.Millisecond)
        }
        ended, emitted := false, false
        var localErr error
        attemptCtx, cancelAttempt := context.WithCancel(ctx)
        var budgetTimer *time.Timer
        if !recoveryDeadline.IsZero() && recovering {
            budgetTimer = time.AfterFunc(max(0, time.Until(recoveryDeadline)), cancelAttempt)
        }
        err = s.Remote.Follow(attemptCtx, ref.Connection, ref.Runner, req, batch, func(event wire.LogEvent) error {
            if event.Record != nil && !wire.CompatibleVersion(wire.Version, event.Record.Request.Version) {
                return versionMismatch(event.Record.Request.Version)
            }
            if !matching(ref, event.Record) {
                return errors.New("BAD_RESPONSE")
            }
            if recover {
                c, decodeErr := wire.DecodeLogCursor(event.Log.Cursor)
                if decodeErr != nil || !c.Matches(req) || c.Offset != event.Log.Offset {
                    return errors.New("BAD_RESPONSE")
                }
                if c.Offset < confirmedOffset || (c.Offset == confirmedOffset && event.Log.Text != "") || (confirmedFile != "" && c.FileID != confirmedFile) {
                    return errors.New("BAD_RESPONSE")
                }
            }
            next := view(ref, event.Record)
            next.Log = &event.Log
            next.ObservedAt = time.Now().UTC()
            confirm := ref.Submission == "uncertain" && next.Status != "unknown" && accepted(ref, event.Record)
            if confirm {
                bound.Submission, ref.Submission = "submitted", "submitted"
                next.Submission, next.Ref = "submitted", bound
            }
            // The execution fact is known before the consumer accepts the log.
            // Keep only the previous consumed chunk in the returned log/cursor.
            consumedLog := last.Log
            last = next
            last.Log = consumedLog
            if confirm {
                saved, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
                saveErr := s.Index.SaveRun(saved, bound)
                cancelSave()
                if saveErr != nil {
                    localErr = recordStateError(&last, saveErr)
                    return localErr
                }
            }
            if emit != nil {
                if err := emit(next, event.Type); err != nil {
                    var known *fault.Error
                    localErr = err
                    if !errors.As(err, &known) {
                        localErr = fault.Wrap("LOG_CONSUMER_FAILED", "log_consumer_failed", err)
                    }
                    return localErr
                }
            }
            if budgetTimer != nil {
                budgetTimer.Stop()
            }
            if recovering {
                if !recoveryDeadline.IsZero() {
                    remainingRecovery = max(0, time.Until(recoveryDeadline))
                    recoveryDeadline = time.Time{}
                }
                recovery.notify("recovered")
                recovering = false
            }
            last = next
            emitted = true
            if event.Log.Cursor != "" {
                cursor = event.Log.Cursor
                if recover {
                    c, _ := wire.DecodeLogCursor(cursor)
                    confirmedOffset, confirmedFile = c.Offset, c.FileID
                }
            }
            ended = event.Type == "end"
            return nil
        })
        if budgetTimer != nil {
            budgetTimer.Stop()
        }
        cancelAttempt()
        if localErr != nil {
            return last, localErr
        }
        if err == nil {
            return last, nil
        }
        if recovery.Budget > 0 && !recoveryDeadline.IsZero() && time.Until(recoveryDeadline) <= 0 {
            recovery.notify("exhausted")
            err = fault.Wrap("LOG_RECOVERY_EXHAUSTED", "log_recovery_exhausted", err)
            break
        }
        if !recover || ended || (emitted && cursor == "") || ctx.Err() != nil || !ssh.IsTransientReadError(err) {
            break
        }
        if recovery.Budget == 0 && attempt > 0 {
            recovery.notify("exhausted")
            break
        }
        if recovery.Budget > 0 {
            if recoveryDeadline.IsZero() {
                recoveryDeadline = time.Now().Add(remainingRecovery)
            }
            if time.Until(recoveryDeadline) <= 0 {
                recovery.notify("exhausted")
                err = fault.Wrap("LOG_RECOVERY_EXHAUSTED", "log_recovery_exhausted", err)
                break
            }
        }
        if !recovering {
            recovery.notify("reconnecting")
            recovering = true
        }
        delay := min(2*time.Second, 250*time.Millisecond*time.Duration(1<<min(attempt, 3)))
        if !recoveryDeadline.IsZero() {
            delay = min(delay, time.Until(recoveryDeadline))
        }
        timer := time.NewTimer(delay)
        select {
        case <-ctx.Done():
            timer.Stop()
        case <-timer.C:
        }
        if ctx.Err() != nil {
            break
        }
    }
    if ctx.Err() != nil {
        return last, fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", ctx.Err())
    }
    queryErr = remoteFault(err)
    last.QueryError = errorCode(queryErr)
    return last, queryErr
}

func errorCode(err error) string {
    var remote *ssh.RemoteError
    if errors.As(err, &remote) {
        return remote.Code
    }
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return "QUERY_FAILED"
}
func remoteFault(err error) error {
    var f *fault.Error
    if errors.As(err, &f) {
        return err
    }
    if errorCode(err) == "BAD_LOG_CURSOR" {
        return fault.New("BAD_CONFIG", "log_cursor_invalid", nil)
    }
    if errorCode(err) == "RUN_ABANDONED" {
        return abandonFault("RUN_ABANDONED", err)
    }
    if errorCode(err) == "LOG_CHANGED" {
        f := fault.New("LOG_CHANGED", "log_changed", nil)
        f.Hint = "log_changed_hint"
        return f
    }
    if errorCode(err) == "VERSION_MISMATCH" {
        return versionMismatch("unknown")
    }
    f = fault.Wrap(errorCode(err), "run_query", err)
    f.Hint = "run_query_hint"
    return f
}
