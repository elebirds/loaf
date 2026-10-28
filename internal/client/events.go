package client

import (
    "context"
    "errors"
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
    LookupEnv    func(string) (string, bool)
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
        s.dependencies.Sender = defaultNotificationSender(d.LookupEnv)
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
    page, err := s.read(ctx, notifications.Source{Source: source, Binding: binding}, read, in.Policy)
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

func (s *eventServices) read(ctx context.Context, sub notifications.Source, in event.ReadInput, policy CallPolicy) (event.Page, error) {
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
    if !config.ValidName(in.Name) || in.Destination.Validate() != nil || in.Destination.Kind == "claude-resume" || in.Destination.BearerRef != "" && !bearerEnvironment.MatchString(in.Destination.BearerRef) {
        return DestinationRecord{}, asyncError("DESTINATION_INVALID", agentdelivery.ErrDestination)
    }
    destination := notifications.Destination{Name: in.Name, Address: savedAddress(in.Destination), CreatedAt: time.Now().UTC()}
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
        if saved.Address == savedAddress(destination) {
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
    return DestinationRecord{Name: d.Name, Kind: d.Address.Kind, SessionID: d.Address.SessionID, CreatedAt: d.CreatedAt, Experimental: d.Address.Kind == "codex-desktop"}
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
        page, err := s.read(ctx, notifications.Source{Source: source, Binding: binding}, event.ReadInput{Source: source, Limit: 1000, Subject: &in.Subject}, in.Policy)
        if err != nil || page.HasMore {
            return result, asyncError("EVENT_FAILED", err)
        }
        initial = &page
    }
    if err = s.store.AddSubscription(ctx, sub, initial); err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    saved, err := s.store.Subscription(ctx, sub.ID)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    return subscriptionRecord(saved), nil
}

func subscriptionRecord(sub notifications.Subscription) Subscription {
    names := make([]string, 0, len(sub.Destinations))
    for _, d := range sub.Destinations {
        names = append(names, d.Name)
    }
    result := Subscription{ID: sub.ID, Subject: sub.Subject, Source: sub.Source, Destinations: names, CreatedAt: sub.CreatedAt, Status: sub.Status, Sources: []SubscriptionSource{}}
    for _, src := range sub.Sources {
        result.Sources = append(result.Sources, SubscriptionSource{ID: src.ID, Source: src.Source, Subject: src.Subject, LastSyncedAt: src.LastSyncedAt, SourceError: src.SourceError, SourceErrorAt: src.SourceErrorAt})
        if src.LastSyncedAt != nil && (result.LastSyncedAt == nil || src.LastSyncedAt.After(*result.LastSyncedAt)) {
            result.LastSyncedAt = src.LastSyncedAt
        }
        if src.SourceErrorAt != nil && (result.SourceErrorAt == nil || src.SourceErrorAt.After(*result.SourceErrorAt)) {
            result.SourceError, result.SourceErrorAt = src.SourceError, src.SourceErrorAt
        }
    }
    if len(sub.Sources) > 1 {
        result.LastSyncedAt = nil
        result.SourceError = ""
        result.SourceErrorAt = nil
    }
    return result
}

// sourceReadError leaves a long-running Bridge alive while the source recovers.
type sourceReadError struct{ error }

func (e sourceReadError) Unwrap() error { return e.error }

func (s *eventServices) ListSubscriptions(ctx context.Context, in SubscriptionListInput) (SubscriptionPage, error) {
    if in.Limit == 0 {
        in.Limit = 100
    }
    rows, more, err := s.store.ListSubscriptions(ctx, in.After, in.Limit)
    result := SubscriptionPage{Subscriptions: []Subscription{}, HasMore: more}
    for _, sub := range rows {
        result.Subscriptions = append(result.Subscriptions, subscriptionRecord(sub))
    }
    if more && len(rows) > 0 {
        result.NextAfter = rows[len(rows)-1].ID
    }
    if err != nil {
        err = asyncError("NOTIFY_FAILED", err)
    }
    return result, err
}

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
    result := Delivery{ID: d.ID, EventID: d.Event.ID, SubscriptionID: d.SubscriptionID, Destination: d.Destination.Name, Receipt: transportEvidence(d.Receipt), Attempts: d.Attempts, UpdatedAt: d.UpdatedAt, ErrorCode: d.ErrorCode, State: d.Stage}
    result.ParentID = d.ParentID
    if d.Resolution != nil {
        result.Resolution = &UserResolution{Kind: d.Resolution.Kind, At: d.Resolution.At, SuccessorID: d.Resolution.SuccessorID}
    }
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
