package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

// EventListInput selects local inbox or an explicit project/queue source.
type EventListInput struct {
    Target  *string
    Source  string
    Cursor  string
    Limit   int
    Subject *event.Subject
    Policy  CallPolicy
}

// EventService exposes read-only, bounded source facts.
type EventService interface {
    List(context.Context, EventListInput) (event.Page, error)
}

// DestinationInput registers an explicitly addressed, private delivery binding.
type DestinationInput struct {
    Name        string
    Destination agentdelivery.Destination
    Replace     bool
}

// DestinationRecord is public destination metadata, never its private endpoint.
type DestinationRecord struct {
    Name      string    `json:"name"`
    Kind      string    `json:"kind"`
    SessionID string    `json:"session_id,omitempty"`
    CreatedAt time.Time `json:"created_at"`
}

// SubscribeInput freezes a safe subject and the selected destination names.
type SubscribeInput struct {
    Subject      event.Subject
    Source       string
    Target       *string
    Destinations []string
    Replay       bool
    Policy       CallPolicy
}

// Subscription is a durable waiting relationship, not execution authority.
type Subscription struct {
    ID           string        `json:"subscription_id"`
    Subject      event.Subject `json:"subject"`
    Source       string        `json:"source"`
    Destinations []string      `json:"destinations"`
    CreatedAt    time.Time     `json:"created_at"`
}

// Delivery retains per-destination evidence and safe failure classification.
type Delivery struct {
    ID             string                `json:"delivery_id"`
    EventID        string                `json:"event_id"`
    SubscriptionID string                `json:"subscription_id"`
    Destination    string                `json:"destination"`
    Receipt        agentdelivery.Receipt `json:"receipt"`
    Attempts       int                   `json:"attempts"`
    UpdatedAt      time.Time             `json:"updated_at"`
    ErrorCode      string                `json:"error_code,omitempty"`
    State          string                `json:"state,omitempty"`
    NextAttemptAt  *time.Time            `json:"next_attempt_at,omitempty"`
}

// NotifyServeInput controls observation/dispatch lifetime without an output mode.
type NotifyServeInput struct {
    Once     bool
    Interval time.Duration
    Policy   CallPolicy
}

// NotifyServeResult preserves bounded progress independently of delivery outcome.
type NotifyServeResult struct {
    Received    int `json:"received"`
    Attempted   int `json:"attempted"`
    Accepted    int `json:"accepted"`
    Unconfirmed int `json:"unconfirmed"`
    Pending     int `json:"pending"`
}

// NotificationService owns durable waiting and delivery, not Run supervision.
type NotificationService interface {
    PutDestination(context.Context, DestinationInput) (DestinationRecord, error)
    Destinations(context.Context) ([]DestinationRecord, error)
    Subscribe(context.Context, SubscribeInput) (Subscription, error)
    Subscriptions(context.Context) ([]Subscription, error)
    Deliveries(context.Context, int) ([]Delivery, error)
    Serve(context.Context, NotifyServeInput, func(Delivery) error) (NotifyServeResult, error)
}

// QueueEnableInput explicitly enables machine/user admission with fixed slots.
type QueueEnableInput struct {
    Target *string
    Slots  int
    Policy CallPolicy
}

// QueueSubmitInput preserves the direct Run argument and source conventions.
type QueueSubmitInput struct {
    Target *string
    Args   []string
    Name   string
    Tags   []string
    Notify []string
    DryRun bool
    Policy CallPolicy
}

// QueueSubmitResult separates admission from execution and offline preview.
type QueueSubmitResult struct {
    Request *queue.Request        `json:"request,omitempty"`
    Run     *execution.View       `json:"run,omitempty"`
    Preview *execution.RunPreview `json:"preview,omitempty"`
}

// QueueListInput bounds a machine queue observation.
type QueueListInput struct {
    Target *string
    Limit  int
    Policy CallPolicy
}

// QueueIDInput addresses a saved request without changing its frozen target.
type QueueIDInput struct {
    ID     string
    Target *string
    Policy CallPolicy
}

// QueuePauseInput controls new admissions, never already accepted execution.
type QueuePauseInput struct {
    Target *string
    Paused bool
    Policy CallPolicy
}

// QueueCleanInput selects explicit no-start requests for preview or deletion.
type QueueCleanInput struct {
    IDs    []string
    DryRun bool
    Policy CallPolicy
}

// QueueService owns input admission and dispatch, independently of CLI rendering.
type QueueService interface {
    Enable(context.Context, QueueEnableInput) (queue.Result, error)
    Submit(context.Context, QueueSubmitInput, ProgressFunc) (QueueSubmitResult, error)
    List(context.Context, QueueListInput) (queue.Result, error)
    Show(context.Context, QueueIDInput) (queue.Result, error)
    Cancel(context.Context, QueueIDInput) (queue.Result, error)
    SetPaused(context.Context, QueuePauseInput) (queue.Result, error)
    Clean(context.Context, QueueCleanInput) (queue.Result, error)
}
