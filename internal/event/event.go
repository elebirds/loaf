// Package event defines safe facts shared by execution, resources and queues.
// Event sources are independent streams; observation is not execution truth.
package event

import (
    "encoding/base64"
    "encoding/json"
    "errors"
    "io"
    "strings"
    "time"
    "unicode"
)

const Version = 1

var ErrInvalid = errors.New("invalid public event")
var ErrCursor = errors.New("event cursor does not match source")

// Subject identifies a fact's object without requiring a Run or Job.
type Subject struct {
    Kind string `json:"kind"`
    ID   string `json:"id"`
}

// Validate accepts only the public subject domains and opaque identifiers.
func (s Subject) Validate() error {
    switch s.Kind {
    case "run", "queue_request", "resource_operation", "resource":
    default:
        return ErrInvalid
    }
    if !identifier(s.ID) {
        return ErrInvalid
    }
    return nil
}

// Correlation links independent facts without exposing private frozen locators.
type Correlation struct {
    ProjectID      string `json:"project_id,omitempty"`
    TargetID       string `json:"target_id,omitempty"`
    RunID          string `json:"run_id,omitempty"`
    QueueRequestID string `json:"queue_request_id,omitempty"`
    CloudRequestID string `json:"cloud_request_id,omitempty"`
}

// Data is an allowlisted public summary, never a private execution payload.
type Data struct {
    Status     string `json:"status,omitempty"`
    Phase      string `json:"phase,omitempty"`
    Code       string `json:"code,omitempty"`
    ExitCode   *int   `json:"exit_code,omitempty"`
    Signal     string `json:"signal,omitempty"`
    InstanceID string `json:"instance_id,omitempty"`
    Product    string `json:"product,omitempty"`
}

// Envelope is one persisted source fact. Sequence is local to SourceID.
type Envelope struct {
    EventVersion int         `json:"event_version"`
    ID           string      `json:"event_id"`
    SourceID     string      `json:"source_id"`
    Sequence     uint64      `json:"sequence"`
    Type         string      `json:"type"`
    Subject      Subject     `json:"subject"`
    OccurredAt   time.Time   `json:"occurred_at"`
    Correlation  Correlation `json:"correlation"`
    DataVersion  int         `json:"data_version"`
    Data         Data        `json:"data"`
}

// Matches includes a fact explicitly correlated to the requested stable object.
func (e Envelope) Matches(subject Subject) bool {
    if e.Subject == subject {
        return true
    }
    switch subject.Kind {
    case "run":
        return e.Correlation.RunID == subject.ID
    case "queue_request":
        return e.Correlation.QueueRequestID == subject.ID
    case "resource_operation":
        return e.Correlation.CloudRequestID == subject.ID
    }
    return false
}

// Validate checks persisted event identity and bounded public values.
func (e Envelope) Validate() error {
    if e.EventVersion != Version || e.DataVersion != 1 || !identifier(e.ID) || !identifier(e.SourceID) || e.Sequence == 0 || e.OccurredAt.IsZero() || e.Subject.Validate() != nil {
        return ErrInvalid
    }
    switch e.Type {
    case "run.accepted", "run.succeeded", "run.failed", "run.stopped", "queue.enqueued", "queue.dispatched", "queue.canceled", "queue.attention_required", "resource.acquired", "resource.ready", "resource.started", "resource.stopped", "resource.attention_required", "attention.required":
    default:
        return ErrInvalid
    }
    for _, s := range []string{e.Correlation.ProjectID, e.Correlation.TargetID, e.Correlation.RunID, e.Correlation.QueueRequestID, e.Correlation.CloudRequestID, e.Data.Status, e.Data.Phase, e.Data.Code, e.Data.Signal, e.Data.InstanceID, e.Data.Product} {
        if s != "" && !identifier(s) {
            return ErrInvalid
        }
    }
    return nil
}

// Cursor binds a source database incarnation and its last received sequence.
type Cursor struct {
    Version  int    `json:"version"`
    SourceID string `json:"source_id"`
    Sequence uint64 `json:"sequence"`
}

// EncodeCursor produces an opaque position; it is not an authorization token.
func EncodeCursor(c Cursor) (string, error) {
    if c.Version != Version || !identifier(c.SourceID) {
        return "", ErrCursor
    }
    b, err := json.Marshal(c)
    if err != nil {
        return "", err
    }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor rejects malformed, foreign-source and unknown-version positions.
func DecodeCursor(value, source string) (Cursor, error) {
    var c Cursor
    if len(value) > 2048 {
        return c, ErrCursor
    }
    b, err := base64.RawURLEncoding.DecodeString(value)
    if err != nil {
        return c, ErrCursor
    }
    d := json.NewDecoder(strings.NewReader(string(b)))
    d.DisallowUnknownFields()
    if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != Version || !identifier(c.SourceID) || c.SourceID != source {
        return Cursor{}, ErrCursor
    }
    return c, nil
}

// ReadInput selects one bounded source stream without changing its facts.
type ReadInput struct {
    Source  string   `json:"source,omitempty"`
    Cursor  string   `json:"cursor,omitempty"`
    Limit   int      `json:"limit"`
    Subject *Subject `json:"subject,omitempty"`
}

// Page retains explicit coverage and a cursor usable only for this source.
type Page struct {
    SourceID   string     `json:"source_id"`
    Events     []Envelope `json:"events"`
    NextCursor string     `json:"next_cursor,omitempty"`
    HasMore    bool       `json:"has_more"`
}

func identifier(s string) bool {
    if s == "" || len(s) > 256 {
        return false
    }
    for _, r := range s {
        if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_-.:", r) {
            return false
        }
    }
    return true
}
