package client

import (
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "path"
    "path/filepath"
    "slices"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/eventlog"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/state/notifications"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
    "github.com/elebirds/loaf/sdk/agentdelivery"
    "golang.org/x/sys/unix"
)

// EventRemote is the version-checked finite source transport consumed here.
type EventRemote interface {
    ControlResponse(context.Context, config.Target, string, wire.Request, bool) (wire.Response, error)
}

// EventDependencies compose concrete state with narrow source/delivery seams.
type EventDependencies struct {
    State        *local.Store
    Settings     config.Global
    CWD          string
    Runs         *execution.Service
    Remote       EventRemote
    Sender       agentdelivery.Sender
    CurrentCodex agentdelivery.Destination
}

type eventServices struct {
    dependencies EventDependencies
    store        notifications.Store
}

// NewEventServices creates services without starting a worker or contacting an Agent.
func NewEventServices(d EventDependencies) (EventService, NotificationService) {
    s := &eventServices{dependencies: d}
    if d.State != nil {
        s.store.Path = d.State.Path
    }
    if s.dependencies.Sender == nil {
        s.dependencies.Sender = agentdelivery.Client{}
    }
    return s, s
}

// Channel runs the optional native Claude Channel with no ordinary CLI output.
func (s *eventServices) Channel(ctx context.Context, in ChannelInput, streams Streams) error {
    input, ok := streams.In.(io.ReadCloser)
    if !ok || streams.Out == nil {
        return asyncError("BAD_CONFIG", nil)
    }
    if err := agentdelivery.RunClaudeChannel(ctx, in.Socket, in.SessionID, input, streams.Out); err != nil {
        if errors.Is(err, agentdelivery.ErrDestination) {
            return asyncError("BAD_CONFIG", err)
        }
        return asyncError("NOTIFY_FAILED", err)
    }
    return nil
}

func asyncError(code string, cause error) error {
    var existing *fault.Error
    if errors.As(cause, &existing) {
        return cause
    }
    var remote *ssh.RemoteError
    if errors.As(cause, &remote) && remote.Code != "" {
        code = remote.Code
    }
    if errors.Is(cause, event.ErrCursor) {
        code = "EVENT_CURSOR_INVALID"
    }
    return fault.Wrap(code, "async_failed", cause)
}

func validateNotificationNames(ctx context.Context, service NotificationService, names []string) error {
    if len(names) == 0 {
        return nil
    }
    if service == nil || len(names) > 32 {
        return asyncError("DESTINATION_INVALID", nil)
    }
    destinations, err := service.Destinations(ctx)
    if err != nil {
        return err
    }
    for i, name := range names {
        if !config.ValidName(name) || slices.Contains(names[:i], name) {
            return asyncError("DESTINATION_INVALID", nil)
        }
        found := false
        for _, destination := range destinations {
            if destination.Name == name {
                found = true
                break
            }
        }
        if !found {
            return asyncError("DESTINATION_INVALID", nil)
        }
    }
    return nil
}

func (s *eventServices) List(ctx context.Context, in EventListInput) (event.Page, error) {
    result := event.Page{Events: []event.Envelope{}}
    if s.dependencies.State == nil || in.Limit < 1 || in.Limit > 1000 || in.Subject != nil && in.Subject.Validate() != nil {
        return result, asyncError("BAD_CONFIG", event.ErrInvalid)
    }
    read := event.ReadInput{Source: in.Source, Cursor: in.Cursor, Limit: in.Limit, Subject: in.Subject}
    if in.Source == "" || in.Source == "local" {
        if in.Target != nil {
            return result, asyncError("BAD_CONFIG", nil)
        }
        page, err := s.store.Inbox(ctx, read)
        if err != nil {
            err = asyncError("EVENT_FAILED", err)
        }
        return page, err
    }
    if in.Source == "resources" {
        if in.Target != nil {
            return result, asyncError("BAD_CONFIG", nil)
        }
        page, err := s.localFacts(ctx, read)
        if err != nil {
            err = asyncError("EVENT_FAILED", err)
        }
        return page, err
    }
    subject := event.Subject{}
    if in.Subject != nil {
        subject = *in.Subject
    }
    binding, source, err := s.freeze(ctx, SubscribeInput{Source: in.Source, Subject: subject, Target: in.Target}, false)
    if err != nil {
        return result, err
    }
    page, err := s.read(ctx, notifications.Subscription{Source: source, Binding: binding}, read, in.Policy)
    if err != nil {
        err = asyncError("EVENT_FAILED", err)
    }
    return page, err
}

func (s *eventServices) localFacts(ctx context.Context, in event.ReadInput) (event.Page, error) {
    database, err := db.Open(ctx, s.store.Path, false)
    if os.IsNotExist(err) {
        return event.Page{Events: []event.Envelope{}}, nil
    }
    if err != nil {
        return event.Page{}, err
    }
    defer database.Close()
    return eventlog.Read(ctx, database, in)
}

func (s *eventServices) read(ctx context.Context, sub notifications.Subscription, in event.ReadInput, policy CallPolicy) (event.Page, error) {
    if sub.Source == "resources" {
        return s.localFacts(ctx, in)
    }
    ref := sub.Binding
    if ref.Version != "" && ref.Version != wire.Version {
        return event.Page{}, asyncError("VERSION_MISMATCH", nil)
    }
    d := s.dependencies
    if d.Remote == nil {
        return event.Page{}, asyncError("EVENT_FAILED", nil)
    }
    if ref.Runner == "" || !filepath.IsAbs(ref.RemoteDir) {
        if d.Runs == nil || d.Runs.Check == nil || d.Runs.Runner == nil {
            return event.Page{}, asyncError("EVENT_FAILED", nil)
        }
        report, err := d.Runs.Check.Check(ctx, ref.Connection, ref.RemoteDir, policy.Batch)
        if err != nil {
            return event.Page{}, err
        }
        ref.RemoteDir = report.RemoteDir
        ref.Runner, err = d.Runs.Runner(ctx, ref.Connection, report.Arch, policy.Batch)
        if err != nil {
            return event.Page{}, err
        }
    }
    in.Source = sub.Source
    request := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "events", Root: ref.RemoteDir, ProjectID: ref.ProjectID, Events: &in}
    if sub.Source == "queue" && ref.Connection.Cloud != nil {
        cloud := ref.Connection.Cloud.Ref
        request.Queue = &queue.Command{HostBinding: cloud.Provider + ":" + cloud.Product + ":" + cloud.ID}
    }
    response, err := d.Remote.ControlResponse(ctx, ref.Connection, ref.Runner, request, policy.Batch)
    if response.Events == nil {
        if err == nil {
            err = errors.New("BAD_RESPONSE")
        }
        return event.Page{}, err
    }
    return *response.Events, err
}

func (s *eventServices) freeze(ctx context.Context, in SubscribeInput, requireSubject bool) (task.RunRef, string, error) {
    var ref task.RunRef
    source := in.Source
    if source == "" || source == "local" {
        switch in.Subject.Kind {
        case "run":
            source = "project"
        case "queue_request":
            source = "queue"
        case "resource", "resource_operation":
            source = "resources"
        }
    }
    if requireSubject && in.Subject.Validate() != nil {
        return ref, source, asyncError("BAD_CONFIG", event.ErrInvalid)
    }
    if source == "resources" {
        if in.Target != nil || requireSubject && in.Subject.Kind != "resource" && in.Subject.Kind != "resource_operation" {
            return ref, source, asyncError("BAD_CONFIG", event.ErrInvalid)
        }
        return ref, source, nil
    }
    if source != "project" && source != "queue" {
        return ref, source, asyncError("BAD_CONFIG", event.ErrInvalid)
    }
    d := s.dependencies
    if d.State == nil {
        return ref, source, asyncError("NOTIFY_FAILED", nil)
    }
    root, file, err := config.Discover(ctx, d.CWD)
    if err != nil {
        return ref, source, err
    }
    project, err := d.State.Existing(root)
    if err != nil {
        return ref, source, err
    }
    if source == "project" && in.Subject.Kind == "run" {
        if in.Target != nil {
            return ref, source, asyncError("BAD_CONFIG", event.ErrInvalid)
        }
        ref, err = d.State.Run(project.ID, in.Subject.ID)
        return ref, source, err
    }
    name, target, err := config.Resolve(value(in.Target), project, file, d.Settings)
    if err != nil {
        return ref, source, err
    }
    remote := file.RemoteDir
    if remote == "" {
        remote = path.Join(target.RemoteBase, project.ID)
    }
    ref = task.RunRef{ProjectID: project.ID, Target: name, Connection: target, RemoteDir: remote, Version: wire.Version}
    if source == "project" && project.ID == "" {
        return ref, source, asyncError("EVENT_FAILED", nil)
    }
    return ref, source, nil
}

func (s *eventServices) PutDestination(ctx context.Context, in DestinationInput) (DestinationRecord, error) {
    if !config.ValidName(in.Name) || in.Destination.Validate() != nil || in.Destination.Kind == "claude-resume" {
        return DestinationRecord{}, asyncError("DESTINATION_INVALID", agentdelivery.ErrDestination)
    }
    destination := notifications.Destination{Name: in.Name, Address: in.Destination, CreatedAt: time.Now().UTC()}
    if err := s.store.PutDestination(ctx, destination, in.Replace); err != nil {
        return DestinationRecord{}, asyncError("NOTIFY_FAILED", err)
    }
    return destinationRecord(destination), nil
}

// BindCurrent verifies the exact entry-point session before writing its binding.
// It does not infer a session from cwd, recency, or a different live server.
func (s *eventServices) BindCurrent(ctx context.Context, in BindCurrentInput) (CurrentBindingResult, error) {
    result := CurrentBindingResult{}
    if !config.ValidName(in.Name) {
        return result, asyncError("DESTINATION_INVALID", nil)
    }
    destination := s.dependencies.CurrentCodex
    if destination.Kind != "codex-desktop" || destination.Validate() != nil {
        return result, fault.New("CURRENT_SESSION_UNAVAILABLE", "notify_current_context_required", nil)
    }
    capabilities, err := s.dependencies.Sender.Probe(ctx, destination)
    if err != nil || !capabilities.Available || !capabilities.ExistingSession {
        return result, fault.Wrap("CURRENT_SESSION_UNAVAILABLE", "notify_current_unavailable", err)
    }
    verified := time.Now().UTC()
    existing, err := s.store.Destinations(ctx)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    for _, saved := range existing {
        if saved.Name != in.Name {
            continue
        }
        if saved.Address == destination {
            return CurrentBindingResult{Destination: destinationRecord(saved), VerifiedAt: verified}, nil
        }
        if !in.Replace {
            return result, fault.New("DESTINATION_EXISTS", "notify_current_exists", map[string]any{"Value": in.Name})
        }
    }
    saved, err := s.PutDestination(ctx, DestinationInput{Name: in.Name, Destination: destination, Replace: in.Replace})
    if err != nil {
        return result, err
    }
    return CurrentBindingResult{Destination: saved, VerifiedAt: verified}, nil
}

func destinationRecord(d notifications.Destination) DestinationRecord {
    return DestinationRecord{Name: d.Name, Kind: d.Address.Kind, SessionID: d.Address.SessionID, CreatedAt: d.CreatedAt}
}

func (s *eventServices) Destinations(ctx context.Context) ([]DestinationRecord, error) {
    rows, err := s.store.Destinations(ctx)
    result := make([]DestinationRecord, 0, len(rows))
    for _, d := range rows {
        result = append(result, destinationRecord(d))
    }
    if err != nil {
        err = asyncError("NOTIFY_FAILED", err)
    }
    return result, err
}

func (s *eventServices) Subscribe(ctx context.Context, in SubscribeInput) (Subscription, error) {
    result := Subscription{}
    if len(in.Destinations) == 0 || len(in.Destinations) > 32 {
        return result, asyncError("BAD_CONFIG", event.ErrInvalid)
    }
    destinations, err := s.store.Destinations(ctx)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    selected := []notifications.Destination{}
    for i, name := range in.Destinations {
        if !config.ValidName(name) || slices.Contains(in.Destinations[:i], name) {
            return result, asyncError("DESTINATION_INVALID", nil)
        }
        found := false
        for _, d := range destinations {
            if d.Name == name {
                selected = append(selected, d)
                found = true
                break
            }
        }
        if !found {
            return result, asyncError("DESTINATION_INVALID", nil)
        }
    }
    binding, source, err := s.freeze(ctx, in, true)
    if err != nil {
        return result, err
    }
    id, err := notifications.ID("sub_")
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    sub := notifications.Subscription{ID: id, Subject: in.Subject, Source: source, Binding: binding, Destinations: selected, CreatedAt: time.Now().UTC()}
    var initial *event.Page
    if !in.Replay {
        page, err := s.read(ctx, sub, event.ReadInput{Source: source, Limit: 1000, Subject: &in.Subject}, in.Policy)
        if err != nil || page.HasMore {
            return result, asyncError("EVENT_FAILED", err)
        }
        initial = &page
    }
    if err = s.store.AddSubscription(ctx, sub, initial); err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    return subscriptionRecord(sub), nil
}

func subscriptionRecord(sub notifications.Subscription) Subscription {
    names := make([]string, 0, len(sub.Destinations))
    for _, d := range sub.Destinations {
        names = append(names, d.Name)
    }
    return Subscription{ID: sub.ID, Subject: sub.Subject, Source: sub.Source, Destinations: names, CreatedAt: sub.CreatedAt, LastSyncedAt: sub.LastSyncedAt, SourceError: sub.SourceError, SourceErrorAt: sub.SourceErrorAt}
}

// sourceReadError leaves a long-running Bridge alive while the source recovers.
type sourceReadError struct{ error }

func (e sourceReadError) Unwrap() error { return e.error }

func (s *eventServices) Subscriptions(ctx context.Context) ([]Subscription, error) {
    rows, err := s.store.Subscriptions(ctx)
    result := make([]Subscription, 0, len(rows))
    for _, sub := range rows {
        result = append(result, subscriptionRecord(sub))
    }
    if err != nil {
        err = asyncError("NOTIFY_FAILED", err)
    }
    return result, err
}

func deliveryRecord(d notifications.Delivery) Delivery {
    result := Delivery{ID: d.ID, EventID: d.Event.ID, SubscriptionID: d.SubscriptionID, Destination: d.Destination.Name, Receipt: d.Receipt, Attempts: d.Attempts, UpdatedAt: d.UpdatedAt, ErrorCode: d.ErrorCode, State: d.Stage}
    if !d.NextAttemptAt.IsZero() {
        result.NextAttemptAt = &d.NextAttemptAt
    }
    return result
}

func (s *eventServices) Deliveries(ctx context.Context, limit int) ([]Delivery, error) {
    rows, err := s.store.Deliveries(ctx, limit)
    result := make([]Delivery, 0, len(rows))
    for _, d := range rows {
        result = append(result, deliveryRecord(d))
    }
    if err != nil {
        err = asyncError("NOTIFY_FAILED", err)
    }
    return result, err
}

func (s *eventServices) Serve(ctx context.Context, in NotifyServeInput, emit func(Delivery) error) (NotifyServeResult, error) {
    result := NotifyServeResult{}
    if s.store.Path == "" || in.Interval < 0 {
        return result, asyncError("BAD_CONFIG", nil)
    }
    subscriptions, err := s.store.Subscriptions(ctx)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    if len(subscriptions) == 0 {
        return result, nil
    }
    lock, err := os.OpenFile(filepath.Join(filepath.Dir(s.store.Path), "notify-bridge.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    defer lock.Close()
    if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        return result, asyncError("NOTIFY_BUSY", err)
    }
    defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
    if err = s.store.RecoverInterrupted(ctx); err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    interval := in.Interval
    if interval == 0 {
        interval = 5 * time.Second
    }
    for {
        round, roundErr := s.serveOnce(ctx, in, emit)
        result.Received += round.Received
        result.Attempted += round.Attempted
        result.Accepted += round.Accepted
        result.Unconfirmed = round.Unconfirmed
        result.Pending = round.Pending
        var sourceFailure sourceReadError
        if in.Once || roundErr != nil && !errors.As(roundErr, &sourceFailure) {
            if in.Once && roundErr == nil && result.Unconfirmed > 0 {
                roundErr = asyncError("DELIVERY_UNCONFIRMED", agentdelivery.ErrUnconfirmed)
            }
            return result, roundErr
        }
        timer := time.NewTimer(interval)
        select {
        case <-ctx.Done():
            timer.Stop()
            return result, ctx.Err()
        case <-timer.C:
        }
    }
}

func (s *eventServices) serveOnce(ctx context.Context, in NotifyServeInput, emit func(Delivery) error) (NotifyServeResult, error) {
    result := NotifyServeResult{}
    subscriptions, err := s.store.Subscriptions(ctx)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    var first error
    for _, sub := range subscriptions {
        cursor, err := s.store.Cursor(ctx, sub.ID)
        if err != nil {
            if first == nil {
                first = asyncError("NOTIFY_FAILED", err)
            }
            continue
        }
        observe, cancel := context.WithTimeout(ctx, 20*time.Second)
        page, err := s.read(observe, sub, event.ReadInput{Source: sub.Source, Cursor: cursor, Limit: 200, Subject: &sub.Subject}, in.Policy)
        cancel()
        if err != nil {
            readErr := asyncError("EVENT_FAILED", err)
            code := "EVENT_FAILED"
            var classified *fault.Error
            if errors.As(readErr, &classified) {
                code = classified.Code
            }
            if savedErr := s.store.RecordSourceError(ctx, sub, code); savedErr != nil {
                first = asyncError("STATE_WRITE_FAILED", savedErr)
            } else if first == nil {
                first = sourceReadError{readErr}
            }
            continue
        }
        received, err := s.store.Collect(ctx, sub, page)
        result.Received += received
        if err != nil {
            first = asyncError("NOTIFY_FAILED", err)
        }
    }
    pending, err := s.store.Pending(ctx, 200, time.Now())
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    for _, candidate := range pending {
        if err = ctx.Err(); err != nil {
            return result, err
        }
        d, err := s.store.Claim(ctx, candidate)
        if err != nil {
            if first == nil {
                first = asyncError("NOTIFY_FAILED", err)
            }
            continue
        }
        send, cancel := context.WithTimeout(ctx, 20*time.Second)
        receipt, sendErr := s.dependencies.Sender.Deliver(send, d.Destination.Address, agentdelivery.Message{ID: d.ID, Subject: d.Event.Type, Text: eventText(d.Event)})
        cancel()
        result.Attempted++
        if receipt.MessageID != d.ID {
            receipt = agentdelivery.Receipt{MessageID: d.ID, State: "unconfirmed", ReasonCode: "DELIVERY_RECEIPT_INVALID"}
            sendErr = agentdelivery.ErrUnconfirmed
        }
        d.Receipt = receipt
        d.UpdatedAt = time.Now().UTC()
        d.Stage = receipt.State
        d.ErrorCode = ""
        d.NextAttemptAt = time.Time{}
        switch receipt.State {
        case "accepted", "processed":
            result.Accepted++
        case "rejected":
            d.ErrorCode = "DELIVERY_REJECTED"
            if receipt.RetrySafe {
                d.Stage = "pending"
                d.NextAttemptAt = time.Now().UTC().Add(time.Second * time.Duration(1<<min(d.Attempts, 8)))
            }
        default:
            d.Stage = "unconfirmed"
            d.ErrorCode = "DELIVERY_UNCONFIRMED"
            result.Unconfirmed++
        }
        if sendErr != nil && d.ErrorCode == "" {
            d.ErrorCode = "DELIVERY_FAILED"
        }
        save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
        saveErr := s.store.Finish(save, d)
        cancel()
        if emit != nil {
            if emitErr := emit(deliveryRecord(d)); emitErr != nil {
                return result, emitErr
            }
        }
        if saveErr != nil {
            return result, asyncError("STATE_WRITE_FAILED", saveErr)
        }
    }
    if querier, ok := s.dependencies.Sender.(agentdelivery.ReceiptQuerier); ok {
        rows, listErr := s.store.Deliveries(ctx, 200)
        if listErr != nil && first == nil {
            first = asyncError("NOTIFY_FAILED", listErr)
        }
        for _, d := range rows {
            if d.Stage != "unconfirmed" || d.Destination.Address.Kind != "claude-channel" {
                continue
            }
            query, cancel := context.WithTimeout(ctx, 5*time.Second)
            receipt, queryErr := querier.Lookup(query, d.Destination.Address, d.Receipt)
            cancel()
            if queryErr != nil || !receipt.Accepted() || receipt.MessageID != d.ID {
                continue
            }
            d.Receipt = receipt
            d.Stage = receipt.State
            d.ErrorCode = ""
            d.UpdatedAt = time.Now().UTC()
            if saveErr := s.store.Reconcile(ctx, d); saveErr != nil {
                if first == nil {
                    first = asyncError("STATE_WRITE_FAILED", saveErr)
                }
                continue
            }
            result.Accepted++
            if emit != nil {
                if emitErr := emit(deliveryRecord(d)); emitErr != nil {
                    return result, emitErr
                }
            }
        }
    }
    result.Pending, err = s.store.PendingCount(ctx)
    if err != nil && first == nil {
        first = asyncError("NOTIFY_FAILED", err)
    }
    result.Unconfirmed, err = s.store.UnconfirmedCount(ctx)
    if err != nil && first == nil {
        first = asyncError("NOTIFY_FAILED", err)
    }
    return result, first
}

func eventText(e event.Envelope) string {
    text := fmt.Sprintf("Event %s: %s %s (%s).", e.ID, e.Subject.Kind, e.Subject.ID, e.Type)
    if e.Correlation.RunID != "" {
        text += " Run ID: " + e.Correlation.RunID + ". Query its saved status and outputs using Loaf."
    }
    if e.Correlation.CloudRequestID != "" {
        text += " Resource request ID: " + e.Correlation.CloudRequestID + "."
    }
    if e.Data.Code != "" {
        text += " Code: " + e.Data.Code + "."
    }
    return text + " This event reports a fact; it does not grant new execution or spending authority."
}
