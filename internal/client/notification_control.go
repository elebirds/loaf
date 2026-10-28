package client

import (
    "context"
    "errors"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/notifications"
    "github.com/elebirds/loaf/sdk/agentdelivery"
    "time"
)

func (s *eventServices) ShowDelivery(ctx context.Context, id string) (DeliveryDetail, error) {
    d, err := s.store.Delivery(ctx, id)
    if err != nil {
        return DeliveryDetail{}, asyncError("DELIVERY_NOT_FOUND", err)
    }
    rows, err := s.store.Attempts(ctx, id)
    if err != nil {
        return DeliveryDetail{}, asyncError("NOTIFY_FAILED", err)
    }
    result := DeliveryDetail{Recipient: destinationRecord(d.Destination), Delivery: deliveryRecord(d), Event: d.Event, Message: transportMessage(d.Message), Attempts: []DeliveryAttempt{}}
    for _, a := range rows {
        result.Attempts = append(result.Attempts, DeliveryAttempt{Number: a.Number, StartedAt: a.StartedAt, CompletedAt: a.CompletedAt, Receipt: transportEvidence(a.Receipt), ErrorCode: a.ErrorCode})
    }
    return result, nil
}
func (s *eventServices) ReconcileDelivery(ctx context.Context, id string) (Delivery, error) {
    d, err := s.store.Delivery(ctx, id)
    if err != nil {
        return Delivery{}, asyncError("DELIVERY_NOT_FOUND", err)
    }
    querier, ok := s.dependencies.Sender.(agentdelivery.ReceiptQuerier)
    if !ok || d.Destination.Address.Kind != "claude-channel" {
        return deliveryRecord(d), asyncError("DELIVERY_LOOKUP_UNSUPPORTED", agentdelivery.ErrUnsupported)
    }
    if d.Stage != "unconfirmed" {
        return deliveryRecord(d), nil
    }
    query, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    receipt, err := querier.Lookup(query, transportAddress(d.Destination.Address), transportEvidence(d.Receipt))
    if err != nil {
        return deliveryRecord(d), asyncError("DELIVERY_UNCONFIRMED", err)
    }
    if receipt.MessageID != d.ID {
        return deliveryRecord(d), asyncError("DELIVERY_UNCONFIRMED", agentdelivery.ErrUnconfirmed)
    }
    if receipt.Accepted() {
        d.Receipt = savedEvidence(receipt)
        d.Stage = receipt.State
        d.ErrorCode = ""
        d.UpdatedAt = time.Now().UTC()
        if err = s.store.Reconcile(ctx, d); err != nil {
            return deliveryRecord(d), asyncError("STATE_WRITE_FAILED", err)
        }
    }
    return deliveryRecord(d), nil
}
func (s *eventServices) ResolveDelivery(ctx context.Context, id, kind string) (Delivery, error) {
    d, err := s.store.Resolve(ctx, id, kind)
    if err != nil {
        err = asyncError("DELIVERY_RESOLVE_FAILED", err)
    }
    return deliveryRecord(d), err
}
func (s *eventServices) ResendDelivery(ctx context.Context, id string) (Delivery, error) {
    d, err := s.store.Resend(ctx, id)
    if err != nil {
        err = asyncError("DELIVERY_RESEND_FAILED", err)
    }
    return deliveryRecord(d), err
}
func (s *eventServices) ShowSubscription(ctx context.Context, id string) (Subscription, error) {
    sub, err := s.store.Subscription(ctx, id)
    if err != nil {
        err = asyncError("SUBSCRIPTION_NOT_FOUND", err)
    }
    return subscriptionRecord(sub), err
}
func (s *eventServices) ChangeSubscription(ctx context.Context, id, status string) (SubscriptionChange, error) {
    r, err := s.store.ChangeSubscription(ctx, id, status)
    if err != nil {
        err = asyncError("SUBSCRIPTION_CHANGE_FAILED", err)
    }
    return SubscriptionChange{SubscriptionID: r.SubscriptionID, Status: r.Status, InFlight: r.InFlight}, err
}
func (s *eventServices) RemoveDestination(ctx context.Context, name string) error {
    if !config.ValidName(name) {
        return asyncError("DESTINATION_INVALID", nil)
    }
    err := s.store.RemoveDestination(ctx, name)
    var used *notifications.DestinationInUse
    if errors.As(err, &used) {
        return fault.New("DESTINATION_IN_USE", "notify_destination_in_use", map[string]any{"Value": name, "Paths": used.IDs})
    }
    if err != nil {
        return asyncError("NOTIFY_FAILED", err)
    }
    return nil
}
func (s *eventServices) Prune(ctx context.Context, in PruneInput) (PruneResult, error) {
    r, err := s.store.Prune(ctx, in.Before, in.DryRun)
    if err != nil {
        err = asyncError("NOTIFY_FAILED", err)
    }
    return PruneResult{Before: r.Before, DryRun: r.DryRun, Deliveries: r.Deliveries, Inbox: r.Inbox}, err
}
