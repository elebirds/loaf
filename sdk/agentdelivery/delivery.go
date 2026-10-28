// Package agentdelivery delivers explicitly addressed messages to coding agents.
// It owns delivery evidence, not application events, durable queues or agent work.
package agentdelivery

import (
    "context"
    "errors"
    "net/url"
    "path/filepath"
    "strings"
    "unicode"
)

var (
    ErrDestination = errors.New("invalid delivery destination")
    ErrMessage     = errors.New("invalid delivery message")
    ErrUnsupported = errors.New("delivery capability is unsupported")
    ErrUnavailable = errors.New("delivery destination is unavailable")
    ErrUnconfirmed = errors.New("delivery outcome is unconfirmed")
)

// Destination addresses one explicit integration mode and session.
// Endpoint is private connection configuration, not public delivery evidence.
type Destination struct {
    Kind      string `json:"kind"`
    Endpoint  string `json:"endpoint,omitempty"`
    SessionID string `json:"session_id,omitempty"`
    CWD       string `json:"cwd,omitempty"`
}

// Validate rejects ambiguous destinations before transport or agent activity.
func (d Destination) Validate() error {
    switch d.Kind {
    case "codex-desktop":
        if !validID(d.SessionID) || !filepath.IsAbs(d.Endpoint) || d.CWD != "" {
            return ErrDestination
        }
    case "codex", "claude-channel", "claude-resume":
        if !validID(d.SessionID) {
            return ErrDestination
        }
        if d.Endpoint != "" && !filepath.IsAbs(d.Endpoint) {
            return ErrDestination
        }
        if d.Kind == "claude-channel" && d.Endpoint == "" {
            return ErrDestination
        }
    case "webhook":
        address, err := url.Parse(d.Endpoint)
        if err != nil || address.Host == "" || address.Scheme != "https" && address.Scheme != "http" || address.User != nil || address.Fragment != "" {
            return ErrDestination
        }
    default:
        return ErrDestination
    }
    if strings.ContainsAny(d.Endpoint+d.CWD, "\x00\r\n") {
        return ErrDestination
    }
    if d.CWD != "" && !filepath.IsAbs(d.CWD) {
        return ErrDestination
    }
    return nil
}

// Capabilities reports evidence a particular destination can actually provide.
type Capabilities struct {
    ExistingSession           bool   `json:"existing_session"`
    Idempotent                bool   `json:"idempotent"`
    ReceiptQueryable          bool   `json:"receipt_queryable"`
    ProcessingAcknowledgement bool   `json:"processing_acknowledgement"`
    Available                 bool   `json:"available"`
    ReasonCode                string `json:"reason_code,omitempty"`
}

// Message contains caller-authorized content and a stable deduplication identity.
type Message struct {
    ID      string `json:"id"`
    Subject string `json:"subject"`
    Text    string `json:"text"`
}

// Validate bounds a message without interpreting its workload semantics.
func (m Message) Validate() error {
    if !validID(m.ID) || m.Text == "" || len(m.Text) > 64<<10 || len(m.Subject) > 512 || strings.ContainsRune(m.Text, 0) {
        return ErrMessage
    }
    return nil
}

// Receipt records the strongest evidenced delivery stage, not agent success.
type Receipt struct {
    MessageID   string `json:"message_id"`
    State       string `json:"state"`
    OperationID string `json:"operation_id,omitempty"`
    ReasonCode  string `json:"reason_code,omitempty"`
    RetrySafe   bool   `json:"retry_safe"`
}

// Accepted reports explicit destination acceptance or processing evidence.
func (r Receipt) Accepted() bool { return r.State == "accepted" || r.State == "processed" }

// Sender is the narrow boundary consumed by a durable application dispatcher.
// Cancellation after a possible send must preserve an unconfirmed receipt.
type Sender interface {
    Probe(context.Context, Destination) (Capabilities, error)
    Deliver(context.Context, Destination, Message) (Receipt, error)
}

// ReceiptQuerier is optional; absence never authorizes an ambiguous replay.
type ReceiptQuerier interface {
    Lookup(context.Context, Destination, Receipt) (Receipt, error)
}

func validID(s string) bool {
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
