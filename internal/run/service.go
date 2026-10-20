package run

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/wire"
)

// RunIndex is the concrete local storage seam consumed by execution.
type RunIndex interface {
    SaveRun(context.Context, config.RunRef) error
    Runs(string) ([]config.RunRef, error)
}

// Remote is the SSH control and observation boundary, independent of rendering.
type Remote interface {
    Control(context.Context, config.Target, string, wire.Request, bool) (*wire.RunRecord, error)
    Observe(context.Context, config.Target, string, wire.Request, bool) (wire.Observation, error)
    Follow(context.Context, config.Target, string, wire.Request, bool, func(wire.LogEvent) error) error
}

// Service composes existing SSH, native source transfer and private run locators.
type Service struct {
    Index  RunIndex
    Check  sources.Checker
    Runner func(context.Context, config.Target, string, bool) (string, error)
    Send   sources.Sender
    Remote Remote
}

// SubmitInput freezes one project's explicit execution inputs.
type SubmitInput struct {
    Project    config.Project
    File       config.ProjectFile
    Target     string
    Connection config.Target
    RemoteDir  string
    Plan       sources.Plan
    Args       []string
    Batch      bool
}

// View exposes operational results without leaking private request environments.
type View struct {
    ID          string         `json:"run_id"`
    Target      string         `json:"target"`
    RemoteDir   string         `json:"remote_dir"`
    Status      string         `json:"status"`
    Phase       string         `json:"phase,omitempty"`
    CreatedAt   time.Time      `json:"created_at"`
    CompletedAt *time.Time     `json:"completed_at,omitempty"`
    ExitCode    *int           `json:"exit_code"`
    Signal      string         `json:"signal,omitempty"`
    Reason      string         `json:"reason,omitempty"`
    QueryError  string         `json:"query_error,omitempty"`
    Log         *wire.LogChunk `json:"log,omitempty"`
    Ref         config.RunRef  `json:"-"`
}

func view(ref config.RunRef, r *wire.RunRecord) View {
    v := View{ID: ref.ID, Target: ref.Target, RemoteDir: ref.RemoteDir, CreatedAt: ref.CreatedAt, Status: "unknown", Ref: ref}
    if r != nil {
        v.Status = r.Status
        v.Phase = r.Phase
        v.CreatedAt = r.CreatedAt
        v.CompletedAt = r.CompletedAt
        v.ExitCode = r.ExitCode
        v.Signal = r.Signal
        v.Reason = r.Reason
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
    req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "launch", ProjectID: in.Project.ID, Args: append([]string(nil), in.Args...), Setup: in.File.Run.Setup, Env: in.File.Run.Env}
    if len(req.Args) == 0 && strings.TrimSpace(in.File.Run.Command) != "" {
        req.Args = []string{"/bin/sh", "-c", in.File.Run.Command}
    }
    if err := validateCommand(req); err != nil {
        return View{}, err
    }
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
    outputs := append([]string(nil), in.File.Outputs.Paths...)
    if len(outputs) == 0 {
        outputs = []string{"outputs"}
    }
    ref := config.RunRef{ID: "r_" + hex.EncodeToString(id[:]), ProjectID: in.Project.ID, Target: in.Target, Connection: in.Connection, RemoteDir: report.RemoteDir, Runner: runner, Version: wire.Version, Outputs: outputs, CreatedAt: time.Now().UTC(), Submission: "preparing"}
    if err := s.Index.SaveRun(ctx, ref); err != nil {
        return View{}, err
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
        return View{}, err
    }
    now := time.Now().UTC()
    ref.AttemptedAt = &now
    ref.Submission = "uncertain"
    if err := s.Index.SaveRun(ctx, ref); err != nil {
        return View{}, err
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
                return view(ref, record), saveErr
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
        f.Hint = "submission_query"
        return result, f
    }
    ref.Submission = "submitted"
    saved, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
    saveErr := s.Index.SaveRun(saved, ref)
    cancelSave()
    return view(ref, record), saveErr
}

func validateCommand(req wire.Request) error {
    if len(req.Args) == 0 || req.Args[0] == "" {
        return fault.New("BAD_CONFIG", "run_command", nil)
    }
    for _, arg := range append(append([]string(nil), req.Args...), req.Setup) {
        if strings.ContainsRune(arg, 0) {
            return fault.New("BAD_CONFIG", "run_command", nil)
        }
    }
    for key, value := range req.Env {
        if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
            return fault.New("BAD_CONFIG", "run_env", nil)
        }
        for i, r := range key {
            if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
                return fault.New("BAD_CONFIG", "run_env", nil)
            }
        }
    }
    data, err := json.Marshal(req)
    if err != nil || len(data) > 900*1024 {
        return fault.New("BAD_CONFIG", "run_command", nil)
    }
    return nil
}

func definiteRejection(code string) bool {
    switch code {
    case "BAD_COMMAND", "BAD_ENV", "BAD_FORMAT", "VERSION_MISMATCH", "UNSAFE_PATH", "PROJECT_MISMATCH", "RUN_NOT_FOUND", "COPY_FAILED", "START_FAILED":
        return true
    }
    return false
}

func matching(ref config.RunRef, r *wire.RunRecord) bool {
    return r != nil && r.FormatVersion == wire.FormatVersion && r.Request.RunID == ref.ID && r.Request.ProjectID == ref.ProjectID && r.Request.Root == ref.RemoteDir
}
func accepted(ref config.RunRef, r *wire.RunRecord) bool {
    return matching(ref, r) && (r.SupervisorPID > 0 || wire.Terminal(r.Status) && r.Phase != "copying")
}

// Select resolves an ID only within the current project. Ambiguity never falls
// back to an older, successful run.
func (s Service) Select(projectID, id string) (config.RunRef, error) {
    refs, err := s.Index.Runs(projectID)
    if err != nil {
        return config.RunRef{}, err
    }
    if id != "" {
        for _, ref := range refs {
            if ref.ID == id {
                return ref, nil
            }
        }
        return config.RunRef{}, fault.New("RUN_NOT_FOUND", "run_not_found", nil)
    }
    if len(refs) == 0 {
        return config.RunRef{}, fault.New("RUN_NOT_FOUND", "run_not_found", nil)
    }
    if len(refs) > 1 && refs[0].AttemptedAt.Equal(*refs[1].AttemptedAt) {
        return config.RunRef{}, fault.New("RUN_AMBIGUOUS", "run_ambiguous", nil)
    }
    return refs[0], nil
}

func request(ref config.RunRef, wait time.Duration, tail int) wire.Request {
    return wire.Request{FormatVersion: wire.FormatVersion, Version: ref.Version, Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, WaitMillis: int64((wait + time.Millisecond - 1) / time.Millisecond), Tail: tail}
}

// Observe queries a bound locator without reading today's target configuration.
func (s Service) Observe(ctx context.Context, ref config.RunRef, wait time.Duration, tail int, batch bool) (View, error) {
    timeout, cancel := context.WithTimeout(ctx, wait+20*time.Second)
    defer cancel()
    observed, err := s.Remote.Observe(timeout, ref.Connection, ref.Runner, request(ref, wait, tail), batch)
    result := view(ref, observed.Record)
    if err != nil {
        if ctx.Err() != nil {
            return result, fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", ctx.Err())
        }
        result.QueryError = errorCode(err)
        return result, remoteFault(err)
    }
    if !matching(ref, observed.Record) {
        return result, fault.New("BAD_RESPONSE", "run_query", nil)
    }
    result.Log = &observed.Log
    return result, nil
}

// Follow observes one run over one SSH connection; callback errors only close it.
func (s Service) Follow(ctx context.Context, ref config.RunRef, wait time.Duration, tail int, batch bool, emit func(View, string) error) (View, error) {
    last := view(ref, nil)
    err := s.Remote.Follow(ctx, ref.Connection, ref.Runner, request(ref, wait, tail), batch, func(event wire.LogEvent) error {
        if !matching(ref, event.Record) {
            return errors.New("BAD_RESPONSE")
        }
        last = view(ref, event.Record)
        last.Log = &event.Log
        return emit(last, event.Type)
    })
    if err != nil {
        if ctx.Err() != nil {
            return last, fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", ctx.Err())
        }
        return last, remoteFault(err)
    }
    return last, nil
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
    f = fault.Wrap(errorCode(err), "run_query", err)
    f.Hint = "run_query_hint"
    return f
}
