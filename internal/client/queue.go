package client

import (
    "bytes"
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "path"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

// QueueRemote is the finite SSH control boundary consumed by admission.
type QueueRemote interface {
    QueueControl(context.Context, config.Target, string, wire.Request, bool) (queue.Result, error)
}

// Queues owns local planning, frozen request bindings and remote admission.
// Projects retains direct Run behavior and needs no queue service.
type Queues struct {
    Projects *Projects
    Remote   QueueRemote
    Notify   NotificationService
}

// NewQueueService composes admission with existing project and transfer services.
func NewQueueService(projects *Projects, remote QueueRemote, notify NotificationService) *Queues {
    return &Queues{Projects: projects, Remote: remote, Notify: notify}
}

type queueBinding struct {
    FormatVersion int           `json:"format_version"`
    Request       queue.Request `json:"request"`
    Run           task.RunRef   `json:"run"`
}

func (q *Queues) services() error {
    if q.Projects == nil || q.Projects.State == nil || q.Projects.Runs == nil || q.Projects.Runs.Check == nil || q.Projects.Runs.Runner == nil || q.Remote == nil {
        return fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    return nil
}

func (q *Queues) target(ctx context.Context, override *string) (string, config.Target, string, error) {
    root, file, err := config.Discover(ctx, q.Projects.CWD)
    if err != nil {
        return "", config.Target{}, "", err
    }
    project, err := q.Projects.State.Existing(root)
    if err != nil {
        return "", config.Target{}, "", err
    }
    name, machine, err := config.Resolve(value(override), project, file, q.Projects.Settings)
    if err != nil {
        return "", machine, "", err
    }
    directory := file.RemoteDir
    if directory == "" {
        directory = machine.RemoteBase
    }
    return name, machine, directory, nil
}

func (q *Queues) prepareConnection(ctx context.Context, machine config.Target, directory string, batch bool) (ssh.Report, string, error) {
    preflight, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    report, err := q.Projects.Runs.Check.Check(preflight, machine, directory, batch)
    if err != nil {
        return report, "", err
    }
    runner, err := q.Projects.Runs.Runner(preflight, machine, report.Arch, batch)
    return report, runner, err
}

func queueHostBinding(machine config.Target) string {
    if machine.Cloud == nil {
        return ""
    }
    ref := machine.Cloud.Ref
    return strings.Join([]string{ref.Provider, ref.Product, ref.ID}, ":")
}

func (q *Queues) call(ctx context.Context, ref task.RunRef, command queue.Command, batch bool) (queue.Result, error) {
    if ref.Version != "" && !wire.CompatibleVersion(wire.Version, ref.Version) {
        return queue.Result{}, queueFailure("VERSION_MISMATCH", nil)
    }
    command.HostBinding = queueHostBinding(ref.Connection)
    request := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "queue", Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, Queue: &command}
    bounded, cancel := context.WithTimeout(ctx, 25*time.Second)
    defer cancel()
    result, err := q.Remote.QueueControl(bounded, ref.Connection, ref.Runner, request, batch)
    expected := command.ID
    if command.Request != nil {
        expected = command.Request.ID
    }
    if result.Request != nil && (expected != "" && result.Request.ID != expected || ref.ID != "" && result.Request.RunID != ref.ID || command.TargetID != "" && result.Request.TargetID != command.TargetID) {
        return queue.Result{}, queueFailure("QUEUE_RESPONSE_MISMATCH", err)
    }
    if result.Status != nil && command.TargetID != "" && result.Status.TargetID != command.TargetID {
        return queue.Result{}, queueFailure("QUEUE_RESPONSE_MISMATCH", err)
    }
    if err == nil {
        return result, nil
    }
    var remote *ssh.RemoteError
    if errors.As(err, &remote) {
        return result, queueFailure(remote.Code, err)
    }
    switch command.Action {
    case "list", "show", "status", "clean-preview":
        return result, err
    case "clean":
        return result, queueFailure("QUEUE_CLEAN_UNCONFIRMED", err)
    case "enqueue":
        return result, queueFailure("QUEUE_ENQUEUE_UNCONFIRMED", err)
    case "prepare":
        return result, queueFailure("QUEUE_PREPARATION_UNCONFIRMED", err)
    case "cancel":
        return result, queueFailure("QUEUE_CANCEL_UNCONFIRMED", err)
    default:
        return result, queueFailure("QUEUE_CONTROL_UNCONFIRMED", err)
    }
}

func queueFailure(code string, cause error) error {
    message := "queue_operation_failed"
    if code == "QUEUE_NOT_ENABLED" {
        message = "queue_not_enabled"
    }
    f := fault.Wrap(code, message, cause)
    f.Params = map[string]any{"Code": code}
    return fault.At(f, "queue")
}

func (q *Queues) Enable(ctx context.Context, in QueueEnableInput) (queue.Result, error) {
    if in.Slots < 1 || in.Slots > 64 {
        f := fault.Invalid("--slots", "out_of_range", "queue_operation_failed")
        f.Params = map[string]any{"Code": "QUEUE_INVALID_SLOTS"}
        return queue.Result{}, f
    }
    if err := q.services(); err != nil {
        return queue.Result{}, err
    }
    name, machine, directory, err := q.target(ctx, in.Target)
    if err != nil {
        return queue.Result{}, err
    }
    report, runner, err := q.prepareConnection(ctx, machine, directory, in.Policy.Batch)
    if err != nil {
        return queue.Result{}, err
    }
    ref := task.RunRef{Target: name, Connection: machine, RemoteDir: report.RemoteDir, Runner: runner, Version: wire.Version}
    return q.call(ctx, ref, queue.Command{Action: "enable", Slots: &in.Slots}, in.Policy.Batch)
}

func (q *Queues) Submit(ctx context.Context, in QueueSubmitInput, emit ProgressFunc) (result QueueSubmitResult, err error) {
    result.Stage = "planning"
    identitySaved := false
    defer func() {
        if err != nil && result.Request != nil {
            result.NextAction = "repair_local_state"
            if identitySaved {
                result.NextAction = "inspect_original_request"
            }
        }
    }()
    // Planning and preview stay entirely local, even when queue/notify are absent.
    if q.Projects == nil {
        return result, fault.New("BAD_CONFIG", "run_command", nil)
    }
    direct := RunInput{Target: in.Target, Args: in.Args, Name: in.Name, Tags: in.Tags, Wait: new("0s"), DryRun: in.DryRun, Policy: in.Policy}
    p, err := q.Projects.planRun(ctx, direct)
    if err != nil {
        return result, err
    }
    if in.DryRun {
        preview := q.Projects.previewRun(direct, p)
        preview.Provenance["wait"] = []string{"default"}
        preview.Notifications = append([]string(nil), in.Notify...)
        result.Preview = &preview
        return result, nil
    }
    if err = validateNotificationNames(ctx, q.Notify, in.Notify); err != nil {
        return result, err
    }
    if err = q.services(); err != nil {
        return result, err
    }
    if q.Projects.Runs.Send == nil {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    p.project, err = q.Projects.State.Select(ctx, p.root, "")
    if err != nil {
        return result, err
    }
    remote := p.file.RemoteDir
    if remote == "" {
        remote = path.Join(p.machine.RemoteBase, p.project.ID)
    }
    progress(emit, Progress{Stage: "queue_checking", Target: p.target})
    report, runner, err := q.prepareConnection(ctx, p.machine, remote, in.Policy.Batch)
    if err != nil {
        return result, err
    }
    probe := task.RunRef{Target: p.target, Connection: p.machine, RemoteDir: report.RemoteDir, Runner: runner, Version: wire.Version}
    status, err := q.call(ctx, probe, queue.Command{Action: "status"}, in.Policy.Batch)
    if err != nil {
        return result, err
    }
    if status.Status == nil || status.Status.TargetID == "" {
        return result, queueFailure("QUEUE_RESPONSE_MISMATCH", nil)
    }
    var random [24]byte
    if _, err = rand.Read(random[:]); err != nil {
        return result, err
    }
    now := time.Now().UTC()
    r := queue.Request{FormatVersion: queue.Version, ID: "q_" + hex.EncodeToString(random[:12]), RunID: "r_" + hex.EncodeToString(random[12:]), ProjectID: p.project.ID, TargetID: status.Status.TargetID, State: "preparing", CreatedAt: now, UpdatedAt: now, Name: p.metadata.Name, Tags: p.metadata.Tags}
    ref := task.RunRef{ID: r.RunID, ProjectID: p.project.ID, Target: p.target, Connection: p.machine, RemoteDir: report.RemoteDir, Runner: runner, Version: wire.Version, Outputs: p.execution.Outputs, CreatedAt: now, Submission: "preparing", Name: p.metadata.Name, Tags: p.metadata.Tags}
    ref.QueueRequestID = r.ID
    result.Request = &r
    result.Run = &execution.View{ID: ref.ID, Version: ref.Version, Target: ref.Target, RemoteDir: ref.RemoteDir, Status: "unknown", Submission: "preparing", CreatedAt: now, Ref: ref}
    result.Stage = "saving_identity"
    if err = q.Projects.State.SaveRun(ctx, ref); err != nil {
        return result, err
    }
    binding, marshalErr := json.Marshal(queueBinding{FormatVersion: 1, Request: r, Run: ref})
    if marshalErr != nil {
        return result, marshalErr
    }
    if err = q.Projects.State.SaveQueueBinding(ctx, r.ID, p.project.ID, binding); err != nil {
        return result, err
    }
    identitySaved = true
    if p.metadata.Name != "" || len(p.metadata.Tags) > 0 {
        if err = q.Projects.State.SaveRunMetadata(ctx, p.project.ID, ref.ID, p.metadata); err != nil {
            return result, err
        }
    }
    if len(in.Notify) > 0 {
        if q.Notify == nil {
            return result, queueFailure("NOTIFY_UNAVAILABLE", nil)
        }
        registrar, ok := q.Notify.(QueuedNotificationRegistrar)
        if !ok {
            return result, queueFailure("NOTIFY_UNAVAILABLE", nil)
        }
        result.Stage = "registering_notifications"
        if _, err = registrar.RegisterQueuedRun(ctx, QueuedNotificationInput{Request: r, Run: ref, Destinations: in.Notify}); err != nil {
            return result, err
        }
    }
    private := queue.Execution{Version: wire.Version, Root: ref.RemoteDir, Args: p.execution.Args, Setup: p.execution.Setup, Env: p.execution.Env, Outputs: p.execution.Outputs}
    result.Stage = "preparing_remote"
    prepared, err := q.call(ctx, ref, queue.Command{Action: "prepare", TargetID: r.TargetID, Request: &r, Execution: &private}, in.Policy.Batch)
    if prepared.Request != nil {
        result.Request = prepared.Request
    }
    if err != nil {
        return result, err
    }
    input := ssh.SyncInput{Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, Mode: p.source.Policy.Mode}
    for _, file := range p.source.Files {
        input.Paths = append(input.Paths, file.Path)
    }
    result.Stage = "syncing"
    progress(emit, Progress{Stage: "queue_syncing", Target: p.target, RunID: ref.ID, Count: len(input.Paths)})
    if _, err = q.Projects.Runs.Send.Sync(ctx, p.machine, runner, p.source.Root, input, in.Policy.Batch); err != nil {
        return result, err
    }
    result.Stage = "enqueueing"
    admitted, err := q.call(ctx, ref, queue.Command{Action: "enqueue", ID: r.ID, TargetID: r.TargetID}, in.Policy.Batch)
    if admitted.Request != nil {
        result.Request = admitted.Request
    }
    if err == nil {
        result.Stage = "admitted"
    }
    return result, err
}

func (q *Queues) binding(ctx context.Context, in QueueIDInput) (queueBinding, error) {
    var binding queueBinding
    if in.ID == "" || in.Target != nil {
        return binding, queueFailure("QUEUE_BAD_REQUEST", nil)
    }
    if q.Projects == nil || q.Projects.State == nil {
        return binding, queueFailure("QUEUE_NOT_FOUND", nil)
    }
    root, _, err := config.Discover(ctx, q.Projects.CWD)
    if err != nil {
        return binding, err
    }
    project, err := q.Projects.State.Existing(root)
    if err != nil {
        return binding, err
    }
    data, err := q.Projects.State.QueueBinding(ctx, in.ID, project.ID)
    if err != nil {
        return binding, queueFailure("QUEUE_NOT_FOUND", err)
    }
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&binding) != nil || binding.FormatVersion != 1 || binding.Request.ID != in.ID || binding.Request.ProjectID != project.ID || binding.Request.RunID != binding.Run.ID || binding.Request.ID != binding.Run.QueueRequestID || !task.ValidRun(binding.Run) {
        return queueBinding{}, queueFailure("QUEUE_BAD_REQUEST", nil)
    }
    return binding, nil
}

func (q *Queues) List(ctx context.Context, in QueueListInput) (queue.Result, error) {
    if in.Limit < 1 || in.Limit > 1000 {
        return queue.Result{}, queueFailure("QUEUE_BAD_REQUEST", nil)
    }
    if err := q.services(); err != nil {
        return queue.Result{}, err
    }
    name, machine, directory, err := q.target(ctx, in.Target)
    if err != nil {
        return queue.Result{}, err
    }
    report, runner, err := q.prepareConnection(ctx, machine, directory, in.Policy.Batch)
    if err != nil {
        return queue.Result{}, err
    }
    return q.call(ctx, task.RunRef{Target: name, Connection: machine, RemoteDir: report.RemoteDir, Runner: runner, Version: wire.Version}, queue.Command{Action: "list", Limit: in.Limit}, in.Policy.Batch)
}

func (q *Queues) byID(ctx context.Context, in QueueIDInput, action string) (queue.Result, error) {
    if q.Remote == nil {
        return queue.Result{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    binding, err := q.binding(ctx, in)
    if err != nil {
        return queue.Result{}, err
    }
    if !wire.CompatibleVersion(wire.Version, binding.Run.Version) {
        return queue.Result{}, queueFailure("VERSION_MISMATCH", nil)
    }
    if err = q.services(); err != nil {
        return queue.Result{}, err
    }
    report, runner, err := q.prepareConnection(ctx, binding.Run.Connection, binding.Run.RemoteDir, in.Policy.Batch)
    if err != nil {
        return queue.Result{}, err
    }
    if report.RemoteDir != binding.Run.RemoteDir {
        return queue.Result{}, queueFailure("QUEUE_IDENTITY_MISMATCH", nil)
    }
    control := binding.Run
    control.Runner = runner
    command := queue.Command{Action: action, ID: binding.Request.ID, TargetID: binding.Request.TargetID}
    if action == "cancel" {
        command.Request = &binding.Request
    }
    result, err := q.call(ctx, control, command, in.Policy.Batch)
    if result.Request != nil && (result.Request.State == "submitted" || result.Request.State == "completed") {
        if result.Request.AttemptedAt == nil {
            return result, queueFailure("QUEUE_RESPONSE_MISMATCH", err)
        }
        ref := binding.Run
        ref.Submission = "submitted"
        ref.AttemptedAt = result.Request.AttemptedAt
        save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
        saveErr := q.Projects.State.SaveRun(save, ref)
        cancel()
        if saveErr != nil {
            var f *fault.Error
            if errors.As(saveErr, &f) {
                result.StateError = f.Code
            } else {
                result.StateError = "WRITE_FAILED"
            }
            return result, saveErr
        }
    }
    return result, err
}
func (q *Queues) Show(ctx context.Context, in QueueIDInput) (queue.Result, error) {
    return q.byID(ctx, in, "show")
}
func (q *Queues) Cancel(ctx context.Context, in QueueIDInput) (queue.Result, error) {
    return q.byID(ctx, in, "cancel")
}

func (q *Queues) SetPaused(ctx context.Context, in QueuePauseInput) (queue.Result, error) {
    if err := q.services(); err != nil {
        return queue.Result{}, err
    }
    name, machine, directory, err := q.target(ctx, in.Target)
    if err != nil {
        return queue.Result{}, err
    }
    report, runner, err := q.prepareConnection(ctx, machine, directory, in.Policy.Batch)
    if err != nil {
        return queue.Result{}, err
    }
    return q.call(ctx, task.RunRef{Target: name, Connection: machine, RemoteDir: report.RemoteDir, Runner: runner, Version: wire.Version}, queue.Command{Action: "pause", Paused: &in.Paused}, in.Policy.Batch)
}

func (q *Queues) Clean(ctx context.Context, in QueueCleanInput) (queue.Result, error) {
    result := queue.Result{Cleaned: []string{}, Requests: []queue.Request{}}
    if len(in.IDs) == 0 || len(in.IDs) > 1000 {
        return result, queueFailure("QUEUE_BAD_REQUEST", nil)
    }
    if q.Remote == nil {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    // Validate the entire selected set before the first destructive request.
    bindings := []queueBinding{}
    seen := map[string]bool{}
    for _, id := range in.IDs {
        if seen[id] {
            continue
        }
        seen[id] = true
        b, err := q.binding(ctx, QueueIDInput{ID: id})
        if err != nil {
            return result, err
        }
        bindings = append(bindings, b)
    }
    if !in.DryRun {
        if err := q.services(); err != nil {
            return result, err
        }
        for i := range bindings {
            b := &bindings[i]
            if !wire.CompatibleVersion(wire.Version, b.Run.Version) {
                return result, queueFailure("VERSION_MISMATCH", nil)
            }
            report, runner, err := q.prepareConnection(ctx, b.Run.Connection, b.Run.RemoteDir, in.Policy.Batch)
            if err != nil {
                return result, err
            }
            if report.RemoteDir != b.Run.RemoteDir {
                return result, queueFailure("QUEUE_IDENTITY_MISMATCH", nil)
            }
            b.Run.Runner = runner
        }
    }
    // Freshly establish eligibility for the complete set before deletion. The
    // remote boundary still revalidates ownership, no-start and locks per item.
    var first error
    for _, binding := range bindings {
        row, err := q.call(ctx, binding.Run, queue.Command{Action: "clean-preview", ID: binding.Request.ID, TargetID: binding.Request.TargetID}, in.Policy.Batch)
        if row.Request != nil {
            result.Requests = append(result.Requests, *row.Request)
        } else {
            result.Requests = append(result.Requests, binding.Request)
        }
        if first == nil && err != nil {
            first = err
        }
    }
    if in.DryRun || first != nil {
        return result, first
    }
    result.Requests = []queue.Request{}
    for _, binding := range bindings {
        row, err := q.call(ctx, binding.Run, queue.Command{Action: "clean", ID: binding.Request.ID, TargetID: binding.Request.TargetID}, in.Policy.Batch)
        if row.Request != nil {
            result.Requests = append(result.Requests, *row.Request)
        } else {
            result.Requests = append(result.Requests, binding.Request)
        }
        result.Cleaned = append(result.Cleaned, row.Cleaned...)
        if first == nil && err != nil {
            first = err
        }
    }
    return result, first
}
