package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/task"
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
    Name         string    `json:"name"`
    Kind         string    `json:"kind"`
    SessionID    string    `json:"session_id,omitempty"`
    CreatedAt    time.Time `json:"created_at"`
    Experimental bool      `json:"experimental"`
}

// BindCurrentInput names the current, explicitly supplied desktop session.
type BindCurrentInput struct {
    Name    string
    Replace bool
}

// CurrentBindingResult reports successful persistence after a read-only probe.
type CurrentBindingResult struct {
    Destination DestinationRecord `json:"destination"`
    VerifiedAt  time.Time         `json:"verified_at"`
}

// CurrentBindingService optionally verifies and saves the calling session.
type CurrentBindingService interface {
    BindCurrent(context.Context, BindCurrentInput) (CurrentBindingResult, error)
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

// SubscriptionListInput pages logical intents, including disabled/removed history.
type SubscriptionListInput struct {
    After string
    Limit int
}

// SubscriptionPage reports explicit coverage and the next subscription ID.
type SubscriptionPage struct {
    Subscriptions []Subscription `json:"subscriptions"`
    HasMore       bool           `json:"has_more"`
    NextAfter     string         `json:"next_after,omitempty"`
}

// Subscription is a durable waiting relationship, not execution authority.
// SubscriptionSource exposes one frozen stream's independent observation evidence.
type SubscriptionSource struct {
    ID            string        `json:"binding_id"`
    Source        string        `json:"source"`
    Subject       event.Subject `json:"subject"`
    LastSyncedAt  *time.Time    `json:"last_synced_at,omitempty"`
    SourceError   string        `json:"source_error,omitempty"`
    SourceErrorAt *time.Time    `json:"source_error_at,omitempty"`
}

// RunNotificationInput cannot redirect an already frozen Run.
type RunNotificationInput struct {
    Run          task.RunRef
    Destinations []string
}

// QueuedNotificationInput registers both sources of one fixed queue submission.
type QueuedNotificationInput struct {
    Request      queue.Request
    Run          task.RunRef
    Destinations []string
}

// ResourceNotificationInput observes a resource or operation without requiring a Run.
type ResourceNotificationInput struct {
    Subject      event.Subject
    Destinations []string
}

// Typed registration seams are owned by their callers, not a generic target-bearing request.
type RunNotificationRegistrar interface {
    RegisterRun(context.Context, RunNotificationInput) (Subscription, error)
}

// QueuedNotificationRegistrar is the queue caller's atomic registration seam.
type QueuedNotificationRegistrar interface {
    RegisterQueuedRun(context.Context, QueuedNotificationInput) (Subscription, error)
}

// ResourceNotificationRegistrar records waiting before a resource mutation.
type ResourceNotificationRegistrar interface {
    RegisterResource(context.Context, ResourceNotificationInput) (Subscription, error)
}

type Subscription struct {
    Status        string               `json:"status"`
    Sources       []SubscriptionSource `json:"sources"`
    ID            string               `json:"subscription_id"`
    Subject       event.Subject        `json:"subject"`
    Source        string               `json:"source"`
    Destinations  []string             `json:"destinations"`
    CreatedAt     time.Time            `json:"created_at"`
    LastSyncedAt  *time.Time           `json:"last_synced_at,omitempty"`
    SourceError   string               `json:"source_error,omitempty"`
    SourceErrorAt *time.Time           `json:"source_error_at,omitempty"`
}

// UserResolution is an explicit human decision, not a transport observation.
type UserResolution struct {
    Kind        string    `json:"kind"`
    At          time.Time `json:"at"`
    SuccessorID string    `json:"successor_id,omitempty"`
}

// DeliveryAttempt exposes a persisted possible-send boundary.
type DeliveryAttempt struct {
    Number      int                   `json:"number"`
    StartedAt   time.Time             `json:"started_at"`
    CompletedAt *time.Time            `json:"completed_at,omitempty"`
    Receipt     agentdelivery.Receipt `json:"receipt"`
    ErrorCode   string                `json:"error_code,omitempty"`
}

// DeliveryDetail is a safe single-delivery inspection, with no private endpoint.
type DeliveryDetail struct {
    Recipient DestinationRecord     `json:"recipient"`
    Delivery  Delivery              `json:"delivery"`
    Event     event.Envelope        `json:"event"`
    Message   agentdelivery.Message `json:"message"`
    Attempts  []DeliveryAttempt     `json:"attempts"`
}

// SubscriptionChange reports state and already claimed sends that cannot be recalled.
type SubscriptionChange struct {
    SubscriptionID string `json:"subscription_id"`
    Status         string `json:"status"`
    InFlight       int    `json:"in_flight"`
}

// PruneInput selects a settled local history cutoff; no default deletion is implied.
type PruneInput struct {
    Before time.Time
    DryRun bool
}

// PruneResult reports exact deletion or preview counts.
type PruneResult struct {
    Before     time.Time `json:"before"`
    DryRun     bool      `json:"dry_run"`
    Deliveries int       `json:"deliveries"`
    Inbox      int       `json:"inbox"`
}

// NotificationControlService manages delivery evidence and intent lifecycle, never workloads.
type NotificationControlService interface {
    ShowDelivery(context.Context, string) (DeliveryDetail, error)
    ReconcileDelivery(context.Context, string) (Delivery, error)
    ResolveDelivery(context.Context, string, string) (Delivery, error)
    ResendDelivery(context.Context, string) (Delivery, error)
    ShowSubscription(context.Context, string) (Subscription, error)
    ChangeSubscription(context.Context, string, string) (SubscriptionChange, error)
    RemoveDestination(context.Context, string) error
    Prune(context.Context, PruneInput) (PruneResult, error)
}

// Delivery retains per-destination evidence and safe failure classification.
type Delivery struct {
    ParentID       string                `json:"parent_id,omitempty"`
    Resolution     *UserResolution       `json:"resolution,omitempty"`
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
    Rejected    int `json:"rejected"`
}

// NotificationService owns durable waiting and delivery, not Run supervision.
type NotificationService interface {
    PutDestination(context.Context, DestinationInput) (DestinationRecord, error)
    Destinations(context.Context) ([]DestinationRecord, error)
    Subscribe(context.Context, SubscribeInput) (Subscription, error)
    Subscriptions(context.Context) ([]Subscription, error)
    ListSubscriptions(context.Context, SubscriptionListInput) (SubscriptionPage, error)
    Deliveries(context.Context, int) ([]Delivery, error)
    Serve(context.Context, NotifyServeInput, func(Delivery) error) (NotifyServeResult, error)
}

// ChannelInput binds an explicitly enabled Claude MCP session to a private socket.
type ChannelInput struct {
    Socket    string
    SessionID string
}

// ChannelService runs only the native MCP stdio protocol on the supplied streams.
// It is optional and does not extend the durable notification service contract.
type ChannelService interface {
    Channel(context.Context, ChannelInput, Streams) error
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
    Stage      string                `json:"stage,omitempty"`
    NextAction string                `json:"next_action,omitempty"`
    Request    *queue.Request        `json:"request,omitempty"`
    Run        *execution.View       `json:"run,omitempty"`
    Preview    *execution.RunPreview `json:"preview,omitempty"`
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
