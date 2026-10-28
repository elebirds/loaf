package client

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "slices"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
    queuestate "github.com/elebirds/loaf/internal/state/queue"
    "github.com/elebirds/loaf/internal/wire"
)

type queueCheck struct{ targets []string }

func (c *queueCheck) Check(_ context.Context, target config.Target, directory string, _ bool) (ssh.Report, error) {
    c.targets = append(c.targets, target.Destination)
    return ssh.Report{RemoteDir: directory, OS: "Linux", Arch: "x86_64", Writable: true}, nil
}

type queueTransfer struct {
    order     *[]string
    snapshots map[string][]byte
    err       error
}

func (s *queueTransfer) Sync(_ context.Context, _ config.Target, _ string, root string, input ssh.SyncInput, _ bool) (ssh.Transfer, error) {
    *s.order = append(*s.order, "sync")
    if s.err != nil {
        return ssh.Transfer{}, s.err
    }
    data, err := os.ReadFile(filepath.Join(root, "input.txt"))
    if err != nil {
        return ssh.Transfer{}, err
    }
    s.snapshots[input.RunID] = slices.Clone(data)
    return ssh.Transfer{}, nil
}

type queueRemote struct {
    store       *queuestate.Store
    order       *[]string
    transfer    *queueTransfer
    loseEnqueue bool
    targets     []string
    onShow      func()
}

func (r *queueRemote) QueueControl(ctx context.Context, target config.Target, _ string, req wire.Request, _ bool) (queue.Result, error) {
    r.targets = append(r.targets, target.Destination)
    c := req.Queue
    *r.order = append(*r.order, c.Action)
    var result queue.Result
    var err error
    switch c.Action {
    case "status":
    case "prepare":
        e, eErr := r.store.Prepare(ctx, *c.Request, *c.Execution)
        result.Request = &e.Request
        err = eErr
    case "enqueue":
        if _, ok := r.transfer.snapshots[req.RunID]; !ok {
            return result, errors.New("input not transferred")
        }
        e, eErr := r.store.Admit(ctx, c.ID)
        result.Request = &e.Request
        err = eErr
        if r.loseEnqueue {
            result.Request = nil
            return result, errors.New("confirmation lost")
        }
    case "show":
        e, eErr := r.store.Entry(ctx, c.ID)
        result.Request = &e.Request
        err = eErr
        if r.onShow != nil {
            r.onShow()
        }
    case "cancel":
        var e queuestate.Entry
        var eErr error
        if c.Request != nil {
            e, eErr = r.store.CancelUnprepared(ctx, *c.Request, queue.Execution{Version: wire.Version, Root: req.Root})
        } else {
            e, eErr = r.store.Cancel(ctx, c.ID)
        }
        result.Request = &e.Request
        err = eErr
    case "list":
        entries, more, eErr := r.store.List(ctx, c.Limit)
        err = eErr
        result.HasMore = more
        for _, e := range entries {
            result.Requests = append(result.Requests, e.Request)
        }
    case "clean-preview", "clean":
        e, eErr := r.store.Entry(ctx, c.ID)
        result.Request = &e.Request
        err = eErr
        if err == nil && e.Request.State != "canceled" {
            return result, &ssh.RemoteError{Code: "QUEUE_NOT_CANCELABLE"}
        }
        if err == nil && c.Action == "clean" {
            delete(r.transfer.snapshots, e.Request.RunID)
            result.Cleaned = []string{e.Request.ID}
        }
    default:
        return result, errors.New("unexpected action")
    }
    status, statusErr := r.store.Status(ctx)
    result.Status = &status
    if err == nil {
        err = statusErr
    }
    return result, err
}

type queueNotifications struct {
    order *[]string
    err   error
}

func (n *queueNotifications) PutDestination(context.Context, DestinationInput) (DestinationRecord, error) {
    return DestinationRecord{}, nil
}
func (n *queueNotifications) Destinations(context.Context) ([]DestinationRecord, error) {
    return []DestinationRecord{{Name: "agent"}}, nil
}
func (n *queueNotifications) RegisterQueuedRun(_ context.Context, in QueuedNotificationInput) (Subscription, error) {
    *n.order = append(*n.order, "notify:queued")
    return Subscription{}, n.err
}
func (n *queueNotifications) Subscribe(_ context.Context, in SubscribeInput) (Subscription, error) {
    *n.order = append(*n.order, "notify:"+in.Source)
    return Subscription{}, n.err
}
func (n *queueNotifications) ListSubscriptions(context.Context, SubscriptionListInput) (SubscriptionPage, error) {
    return SubscriptionPage{}, nil
}
func (n *queueNotifications) Subscriptions(context.Context) ([]Subscription, error) { return nil, nil }
func (n *queueNotifications) Deliveries(context.Context, int) ([]Delivery, error)   { return nil, nil }
func (n *queueNotifications) Serve(context.Context, NotifyServeInput, func(Delivery) error) (NotifyServeResult, error) {
    return NotifyServeResult{}, nil
}

func queueClientFixture(t *testing.T) (*Queues, *queueRemote, *queueTransfer, *queueNotifications, *queueCheck, string) {
    t.Helper()
    root := t.TempDir()
    if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte("target='lab'\n[run]\ncommand='python3 train.py'\n[run.env]\nTOKEN='not-public'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("original"), 0600); err != nil {
        t.Fatal(err)
    }
    settings := config.Global{Targets: map[string]config.Target{"lab": {Destination: "original-host", RemoteBase: filepath.Join(t.TempDir(), "projects")}}}
    state := local.New(t.TempDir())
    order := []string{}
    transfer := &queueTransfer{order: &order, snapshots: map[string][]byte{}}
    checker := &queueCheck{}
    runs := &execution.Service{Index: state, Metadata: state, Check: checker, Runner: func(context.Context, config.Target, string, bool) (string, error) { return "/runner", nil }, Send: transfer}
    project := &Projects{Config: config.New(t.TempDir(), "", ""), State: state, Settings: settings, Runs: runs, CWD: root}
    store, err := queuestate.Open(context.Background(), filepath.Join(t.TempDir(), "queue", "state.sqlite3"), queuestate.Identity{Host: "fixture"}, true)
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { store.Close() })
    remote := &queueRemote{store: store, order: &order, transfer: transfer}
    notifications := &queueNotifications{order: &order}
    return NewQueueService(project, remote, notifications), remote, transfer, notifications, checker, root
}

func TestQueueSubmitFreezesInputAndRegistersBeforeSideEffects(t *testing.T) {
    q, remote, transfer, _, _, root := queueClientFixture(t)
    ctx := context.Background()
    result, err := q.Submit(ctx, QueueSubmitInput{Notify: []string{"agent"}}, nil)
    if err != nil {
        t.Fatal(err)
    }
    if result.Request == nil || result.Request.State != "queued" || result.Run.Submission != "preparing" {
        t.Fatalf("admission confused with execution: %+v", result)
    }
    if !slices.Equal(*remote.order, []string{"status", "notify:queued", "prepare", "sync", "enqueue"}) {
        t.Fatalf("unsafe order: %v", *remote.order)
    }
    os.WriteFile(filepath.Join(root, "input.txt"), []byte("changed"), 0600)
    if string(transfer.snapshots[result.Request.RunID]) != "original" {
        t.Fatal("queued input changed with local edit")
    }
    saved, err := q.Projects.State.Run(result.Request.ProjectID, result.Request.RunID)
    if err != nil || saved.Submission != "preparing" || saved.AttemptedAt != nil {
        t.Fatalf("queued Run falsely marked submitted: %+v %v", saved, err)
    }
    if saved.QueueRequestID != result.Request.ID {
        t.Fatal("Run locator lost its frozen queue identity")
    }
    data, _ := json.Marshal(result)
    if strings.Contains(string(data), "not-public") || strings.Contains(string(data), "TOKEN") {
        t.Fatalf("execution environment leaked: %s", data)
    }
}

func TestQueueListPreservesBoundedCoverage(t *testing.T) {
    q, _, _, _, _, _ := queueClientFixture(t)
    ctx := context.Background()
    first, err := q.Submit(ctx, QueueSubmitInput{}, nil)
    if err != nil {
        t.Fatal(err)
    }
    second, err := q.Submit(ctx, QueueSubmitInput{}, nil)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = q.Cancel(ctx, QueueIDInput{ID: first.Request.ID}); err != nil {
        t.Fatal(err)
    }
    result, err := q.List(ctx, QueueListInput{Limit: 1})
    if err != nil || !result.HasMore || len(result.Requests) != 1 || result.Requests[0].ID != second.Request.ID {
        t.Fatalf("list lost live work or coverage: %+v %v", result, err)
    }
}

func TestQueueLostAdmissionConfirmationRecoversOriginalBinding(t *testing.T) {
    q, remote, transfer, _, checker, _ := queueClientFixture(t)
    ctx := context.Background()
    remote.loseEnqueue = true
    result, err := q.Submit(ctx, QueueSubmitInput{}, nil)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "QUEUE_ENQUEUE_UNCONFIRMED" || result.Request == nil {
        t.Fatalf("lost identity: %+v %v", result, err)
    }
    q.Projects.Settings.Targets = map[string]config.Target{"other": {Destination: "wrong-host", RemoteBase: "/other"}}
    recovered, err := q.Show(ctx, QueueIDInput{ID: result.Request.ID})
    if err != nil || recovered.Request.State != "queued" || recovered.Request.RunID != result.Request.RunID {
        t.Fatalf("did not recover original request: %+v %v", recovered, err)
    }
    if remote.targets[len(remote.targets)-1] != "original-host" || checker.targets[len(checker.targets)-1] != "original-host" {
        t.Fatal("historical request redirected through current aliases")
    }
    count := 0
    for _, action := range *remote.order {
        if action == "enqueue" {
            count++
        }
    }
    if count != 1 || len(transfer.snapshots) != 1 {
        t.Fatal("recovery replayed admission or input transfer")
    }
}

func TestQueueNotificationFailurePreventsRemotePreparationAndUpload(t *testing.T) {
    q, remote, transfer, notifications, _, _ := queueClientFixture(t)
    notifications.err = errors.New("subscription failed")
    result, err := q.Submit(context.Background(), QueueSubmitInput{Notify: []string{"agent"}}, nil)
    if err == nil || result.Request == nil {
        t.Fatalf("missing failure identity: %+v %v", result, err)
    }
    if slices.Contains(*remote.order, "prepare") || len(transfer.snapshots) > 0 {
        t.Fatal("remote side effect preceded durable subscription")
    }
}

func TestQueueSyncFailureKeepsPreparingRequestCancelable(t *testing.T) {
    q, remote, transfer, _, _, _ := queueClientFixture(t)
    transfer.err = errors.New("copy failed")
    result, err := q.Submit(context.Background(), QueueSubmitInput{}, nil)
    if err == nil || result.Request == nil {
        t.Fatalf("missing prepare identity: %+v %v", result, err)
    }
    if slices.Contains(*remote.order, "enqueue") {
        t.Fatal("failed source was admitted")
    }
    canceled, err := q.Cancel(context.Background(), QueueIDInput{ID: result.Request.ID})
    if err != nil || canceled.Request.State != "canceled" {
        t.Fatalf("preparation cannot be canceled: %+v %v", canceled, err)
    }
}

func TestQueueVerifiedDispatchSurvivesLocalWriteFailure(t *testing.T) {
    q, remote, _, _, _, _ := queueClientFixture(t)
    ctx := context.Background()
    result, err := q.Submit(ctx, QueueSubmitInput{}, nil)
    if err != nil {
        t.Fatal(err)
    }
    selected, err := remote.store.Reserve(ctx, queue.FIFO{})
    if err != nil || len(selected) != 1 {
        t.Fatal(err)
    }
    attempted, err := remote.store.MarkAttempted(ctx, result.Request.ID)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = remote.store.Confirm(ctx, result.Request.ID, false); err != nil {
        t.Fatal(err)
    }
    bad := filepath.Join(t.TempDir(), "not-a-directory")
    os.WriteFile(bad, []byte("blocked"), 0600)
    remote.onShow = func() { q.Projects.State.Path = filepath.Join(bad, "state.db") }
    confirmed, err := q.Show(ctx, QueueIDInput{ID: result.Request.ID})
    if err == nil || confirmed.Request == nil || confirmed.Request.State != "submitted" || confirmed.StateError == "" || confirmed.Request.AttemptedAt == nil || !confirmed.Request.AttemptedAt.Equal(*attempted.Request.AttemptedAt) {
        t.Fatalf("verified dispatch erased: %+v %v", confirmed, err)
    }
}

func TestQueueDryRunDoesNotRequireAsyncOrWriteState(t *testing.T) {
    q, _, _, _, _, _ := queueClientFixture(t)
    q.Remote = nil
    q.Notify = nil
    q.Projects.Runs = nil
    result, err := q.Submit(context.Background(), QueueSubmitInput{DryRun: true}, nil)
    if err != nil || result.Preview == nil || result.Request != nil {
        t.Fatalf("preview=%+v error=%v", result, err)
    }
    if _, err = os.Stat(q.Projects.State.Path); !os.IsNotExist(err) {
        t.Fatalf("preview wrote state: %v", err)
    }
}

func TestQueueCleanValidatesAllSelectedRequestsBeforeDeletion(t *testing.T) {
    q, remote, transfer, _, _, _ := queueClientFixture(t)
    ctx := context.Background()
    first, err := q.Submit(ctx, QueueSubmitInput{}, nil)
    if err != nil {
        t.Fatal(err)
    }
    second, err := q.Submit(ctx, QueueSubmitInput{}, nil)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = q.Cancel(ctx, QueueIDInput{ID: first.Request.ID}); err != nil {
        t.Fatal(err)
    }
    result, err := q.Clean(ctx, QueueCleanInput{IDs: []string{first.Request.ID, second.Request.ID}})
    if err == nil || len(result.Cleaned) != 0 || len(transfer.snapshots) != 2 || slices.Contains(*remote.order, "clean") {
        t.Fatalf("ineligible batch partially deleted: %+v %v", result, err)
    }
    result, err = q.Clean(ctx, QueueCleanInput{IDs: []string{first.Request.ID}})
    if err != nil || len(result.Cleaned) != 1 || len(transfer.snapshots) != 1 {
        t.Fatalf("eligible cancellation not reclaimed: %+v %v", result, err)
    }
}

func TestQueueCloudIdentityIsIndependentOfLocalAccountAliases(t *testing.T) {
    ctx := context.Background()
    original := config.Target{Cloud: &cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "old-alias", ID: "instance-1"}}}
    alias := config.Target{Cloud: &cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "new-alias", ID: "instance-1"}}}
    clone := config.Target{Cloud: &cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "old-alias", ID: "instance-2"}}}
    if queueHostBinding(original) != queueHostBinding(alias) {
        t.Fatal("local account aliases split machine identity")
    }
    path := filepath.Join(t.TempDir(), "queue", "state.sqlite3")
    first, err := queuestate.Open(ctx, path, queuestate.Identity{Host: "same-host-uid", Binding: queueHostBinding(original)}, true)
    if err != nil {
        t.Fatal(err)
    }
    defer first.Close()
    same, err := queuestate.Open(ctx, path, queuestate.Identity{Host: "same-host-uid", Binding: queueHostBinding(alias)}, false)
    if err != nil {
        t.Fatal(err)
    }
    defer same.Close()
    if first.TargetID != same.TargetID {
        t.Fatal("same instance split capacity ledger")
    }
    if _, err = queuestate.Open(ctx, path, queuestate.Identity{Host: "same-host-uid", Binding: queueHostBinding(clone)}, true); err == nil {
        t.Fatal("cloned instance inherited donor ledger")
    }
}

type queuedRunObserver struct{ record *wire.RunRecord }

func (r queuedRunObserver) Control(context.Context, config.Target, string, wire.Request, bool) (*wire.RunRecord, error) {
    return r.record, nil
}
func (r queuedRunObserver) Observe(context.Context, config.Target, string, wire.Request, bool) (wire.Observation, error) {
    return wire.Observation{Record: r.record}, nil
}
func (r queuedRunObserver) Follow(context.Context, config.Target, string, wire.Request, bool, func(wire.LogEvent) error) error {
    return nil
}

func TestQueueRunCanBeObservedDirectlyWithoutQueueShow(t *testing.T) {
    for _, phase := range []string{"prepared", "running", "unknown"} {
        t.Run(phase, func(t *testing.T) {
            q, remote, _, _, _, _ := queueClientFixture(t)
            ctx := context.Background()
            result, err := q.Submit(ctx, QueueSubmitInput{}, nil)
            if err != nil {
                t.Fatal(err)
            }
            ref, err := q.Projects.State.Run(result.Request.ProjectID, result.Request.RunID)
            if err != nil {
                t.Fatal(err)
            }
            e, err := remote.store.Entry(ctx, result.Request.ID)
            if err != nil {
                t.Fatal(err)
            }
            record := &wire.RunRecord{FormatVersion: 1, Request: wire.Request{FormatVersion: 1, Version: wire.Version, Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID}, Status: "starting", Phase: "prepared", CreatedAt: ref.CreatedAt}
            if phase != "prepared" {
                selected, err := remote.store.Reserve(ctx, queue.FIFO{})
                if err != nil || len(selected) != 1 {
                    t.Fatal(err)
                }
                e, err = remote.store.MarkAttempted(ctx, result.Request.ID)
                if err != nil {
                    t.Fatal(err)
                }
                if _, err = remote.store.Confirm(ctx, result.Request.ID, false); err != nil {
                    t.Fatal(err)
                }
                record.Request.QueueRequestID = e.Request.ID
                record.Request.AttemptedAt = e.Request.AttemptedAt
                record.Request.Args = e.Execution.Args
                record.Request.Env = e.Execution.Env
                record.Request.Outputs = e.Execution.Outputs
                record.Status = phase
                record.Phase = "command"
                record.SupervisorPID = 123
                record.SupervisorStart = "12345"
                record.BootID = "boot"
                if phase == "unknown" {
                    record.Reason = "SUPERVISOR_LOST"
                }
            }
            q.Projects.Runs.Remote = queuedRunObserver{record: record}
            observed, err := q.Projects.Runs.Observe(ctx, ref, 0, 0, true)
            if err != nil || observed.QueryError != "" {
                t.Fatalf("valid queue-owned Run not observable: %+v %v", observed, err)
            }
            saved, err := q.Projects.State.Run(ref.ProjectID, ref.ID)
            if err != nil {
                t.Fatal(err)
            }
            if phase == "prepared" {
                if saved.Submission != "preparing" || saved.AttemptedAt != nil || observed.Phase != "prepared" {
                    t.Fatalf("input preparation was treated as acceptance: %+v %+v", saved, observed)
                }
            } else {
                if saved.Submission != "submitted" || observed.Submission != "submitted" || saved.AttemptedAt == nil || !saved.AttemptedAt.Equal(*e.Request.AttemptedAt) || observed.Status != phase {
                    t.Fatalf("verified admission lost: %+v %+v", saved, observed)
                }
            }
        })
    }
}
