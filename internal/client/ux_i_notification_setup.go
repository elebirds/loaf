package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/notifications"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

// NotificationDestinationPreview is public read-only evidence, never an endpoint
// or permission to send. Webhook probes validate configuration, not remote health.
type NotificationDestinationPreview struct {
    Destination  DestinationRecord
    Capabilities agentdelivery.Capabilities
    CheckedAt    time.Time
}

// NotificationSetupService supplements the existing mutation interfaces with
// safe pre-save identity and frozen-recipient inspection for interactive callers.
type NotificationSetupService interface {
    PreviewCurrentBinding(context.Context) (NotificationDestinationPreview, error)
    ProbeDestination(context.Context, string) (NotificationDestinationPreview, error)
    SubscriptionRecipients(context.Context, string) ([]DestinationRecord, error)
}

func (s *eventServices) PreviewCurrentBinding(ctx context.Context) (NotificationDestinationPreview, error) {
    d := s.dependencies.CurrentCodex
    if d.Kind != "codex-desktop" || d.Validate() != nil {
        return NotificationDestinationPreview{}, fault.New("CURRENT_SESSION_UNAVAILABLE", "notify_current_context_required", nil)
    }
    return s.previewDestination(ctx, notifications.Destination{Address: savedAddress(d)})
}

func (s *eventServices) ProbeDestination(ctx context.Context, name string) (NotificationDestinationPreview, error) {
    rows, err := s.store.Destinations(ctx)
    if err != nil {
        return NotificationDestinationPreview{}, asyncError("NOTIFY_FAILED", err)
    }
    for _, row := range rows {
        if row.Name == name {
            return s.previewDestination(ctx, row)
        }
    }
    return NotificationDestinationPreview{}, asyncError("DESTINATION_INVALID", nil)
}

func (s *eventServices) previewDestination(ctx context.Context, d notifications.Destination) (NotificationDestinationPreview, error) {
    result := NotificationDestinationPreview{Destination: destinationRecord(d)}
    probe, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    caps, err := s.dependencies.Sender.Probe(probe, transportAddress(d.Address))
    result.Capabilities, result.CheckedAt = caps, time.Now().UTC()
    if err != nil || !caps.Available || d.Address.Kind != "webhook" && !caps.ExistingSession {
        // Adapter diagnostics can contain private transport context. Retain only
        // public capability codes and a stable application failure.
        return result, fault.New("FEATURE_UNAVAILABLE", "async_unavailable", map[string]any{"Value": "notify destination"})
    }
    return result, nil
}

func (s *eventServices) SubscriptionRecipients(ctx context.Context, id string) ([]DestinationRecord, error) {
    sub, err := s.store.Subscription(ctx, id)
    if err != nil {
        return nil, asyncError("SUBSCRIPTION_NOT_FOUND", err)
    }
    result := make([]DestinationRecord, 0, len(sub.Destinations))
    for _, d := range sub.Destinations {
        result = append(result, destinationRecord(d))
    }
    return result, nil
}
