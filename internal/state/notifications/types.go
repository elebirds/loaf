// Package notifications owns the versioned local notification protocol.
// SDK transport values are mapped at the application boundary, never serialized here.
package notifications

import (
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/task"
    "time"
)

// RecordVersion identifies Loaf-owned notification JSON independently of SDK and database versions.
const RecordVersion = 1

// Address is private frozen connection configuration; secrets are never stored.
type Address struct {
    Kind              string
    Endpoint          string
    SessionID         string
    CWD               string
    BearerEnv         string
    AllowInsecureHTTP bool
}

// Evidence records observed transport facts independently of user decisions.
type Evidence struct {
    MessageID   string `json:"message_id"`
    State       string `json:"state"`
    OperationID string `json:"operation_id,omitempty"`
    ReasonCode  string `json:"reason_code,omitempty"`
    RetrySafe   bool   `json:"retry_safe"`
}

// Accepted reports observed destination acceptance, never user resolution.
func (r Evidence) Accepted() bool { return r.State == "accepted" || r.State == "processed" }

// Destination is copied into an intent, so alias replacement cannot redirect it.
type Destination struct {
    Version   int
    Name      string
    Address   Address
    CreatedAt time.Time
}

// Source holds one frozen stream of a logical subscription.
type Source struct {
    Version        int
    ID             string
    SubscriptionID string
    Source         string
    Subject        event.Subject
    Binding        task.RunRef
    NextPollAt     time.Time
    LastSyncedAt   *time.Time
    SourceError    string
    SourceErrorAt  *time.Time
    Failures       int
}

// Subscription is one user intent with one or more independent source cursors.
type Subscription struct {
    Version         int
    ID              string
    RegistrationKey string
    Status          string
    Subject         event.Subject
    Source          string
    Binding         task.RunRef
    Sources         []Source `json:"-"`
    Destinations    []Destination
    CreatedAt       time.Time
    LastSyncedAt    *time.Time
    SourceError     string
    SourceErrorAt   *time.Time
}

// Message freezes exactly what a delivery and its explicit successor convey.
type Message struct {
    ID      string `json:"id"`
    Subject string `json:"subject"`
    Text    string `json:"text"`
}

// Resolution is a user attestation, never an SDK processing acknowledgement.
type Resolution struct {
    Kind        string    `json:"kind"`
    At          time.Time `json:"at"`
    SuccessorID string    `json:"successor_id,omitempty"`
}

// Attempt records a single claimed possible-send boundary and its observed outcome.
type Attempt struct {
    Number      int        `json:"number"`
    StartedAt   time.Time  `json:"started_at"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
    Receipt     Evidence   `json:"receipt"`
    ErrorCode   string     `json:"error_code,omitempty"`
}

// Delivery retains frozen content, recipient, evidence, and explicit lineage.
type Delivery struct {
    Version        int
    ID             string
    SubscriptionID string
    Destination    Destination
    Event          event.Envelope
    Message        Message
    ParentID       string
    Stage          string
    Receipt        Evidence
    Attempts       int
    UpdatedAt      time.Time
    NextAttemptAt  time.Time
    NextLookupAt   time.Time
    ErrorCode      string
    Resolution     *Resolution
}

// SubscriptionChange reports possible sends already claimed before disable/remove.
type SubscriptionChange struct {
    SubscriptionID string `json:"subscription_id"`
    Status         string `json:"status"`
    InFlight       int    `json:"in_flight"`
}

// PruneResult describes a bounded, explicit local history deletion.
type PruneResult struct {
    DryRun     bool      `json:"dry_run"`
    Before     time.Time `json:"before"`
    Deliveries int       `json:"deliveries"`
    Inbox      int       `json:"inbox"`
}
