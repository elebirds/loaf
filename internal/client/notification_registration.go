package client

import (
    "context"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/notifications"
    "github.com/elebirds/loaf/internal/task"
    "time"
)

// RegisterRun accepts only an already frozen Run, never a target override.
func (s *eventServices) RegisterRun(ctx context.Context, in RunNotificationInput) (Subscription, error) {
    if !task.ValidRun(in.Run) {
        return Subscription{}, asyncError("BAD_CONFIG", event.ErrInvalid)
    }
    subject := event.Subject{Kind: "run", ID: in.Run.ID}
    return s.register(ctx, "run:"+in.Run.ID, subject, []notifications.Source{{Source: "project", Subject: subject, Binding: in.Run}}, in.Destinations)
}

// RegisterQueuedRun makes one durable notification intent for both independent streams.
func (s *eventServices) RegisterQueuedRun(ctx context.Context, in QueuedNotificationInput) (Subscription, error) {
    if !task.ValidRun(in.Run) || in.Request.ID != in.Run.QueueRequestID || in.Request.RunID != in.Run.ID || in.Request.ProjectID != in.Run.ProjectID || in.Request.TargetID == "" {
        return Subscription{}, asyncError("BAD_CONFIG", event.ErrInvalid)
    }
    q := event.Subject{Kind: "queue_request", ID: in.Request.ID}
    r := event.Subject{Kind: "run", ID: in.Run.ID}
    return s.register(ctx, "queue:"+in.Request.ID, q, []notifications.Source{{Source: "queue", Subject: q, Binding: in.Run}, {Source: "project", Subject: r, Binding: in.Run}}, in.Destinations)
}

// RegisterResource records waiting independently of Run or scheduling.
func (s *eventServices) RegisterResource(ctx context.Context, in ResourceNotificationInput) (Subscription, error) {
    if in.Subject.Validate() != nil || (in.Subject.Kind != "resource" && in.Subject.Kind != "resource_operation") {
        return Subscription{}, asyncError("BAD_CONFIG", event.ErrInvalid)
    }
    return s.register(ctx, in.Subject.Kind+":"+in.Subject.ID, in.Subject, []notifications.Source{{Source: "resources", Subject: in.Subject}}, in.Destinations)
}
func (s *eventServices) register(ctx context.Context, key string, subject event.Subject, sources []notifications.Source, names []string) (Subscription, error) {
    selected, err := s.selectedDestinations(ctx, names)
    if err != nil {
        return Subscription{}, err
    }
    id, err := notifications.ID("sub_")
    if err != nil {
        return Subscription{}, asyncError("NOTIFY_FAILED", err)
    }
    source := "multiple"
    if len(sources) == 1 {
        source = sources[0].Source
    }
    saved, err := s.store.Register(ctx, notifications.Subscription{ID: id, RegistrationKey: key, Status: "enabled", Subject: subject, Source: source, Sources: sources, Destinations: selected, CreatedAt: time.Now().UTC()}, nil)
    if err != nil {
        return Subscription{}, asyncError("NOTIFY_FAILED", err)
    }
    return subscriptionRecord(saved), nil
}
func (s *eventServices) selectedDestinations(ctx context.Context, names []string) ([]notifications.Destination, error) {
    if len(names) == 0 || len(names) > 32 {
        return nil, asyncError("DESTINATION_INVALID", nil)
    }
    destinations, err := s.store.Destinations(ctx)
    if err != nil {
        return nil, asyncError("NOTIFY_FAILED", err)
    }
    selected := []notifications.Destination{}
    seen := map[string]bool{}
    for _, name := range names {
        if !config.ValidName(name) || seen[name] {
            return nil, asyncError("DESTINATION_INVALID", nil)
        }
        seen[name] = true
        found := false
        for _, d := range destinations {
            if d.Name == name {
                selected = append(selected, d)
                found = true
                break
            }
        }
        if !found {
            return nil, asyncError("DESTINATION_INVALID", nil)
        }
    }
    return selected, nil
}
