package agentdelivery

import "context"

// Client manually composes the supported concrete adapters. It has no registry,
// background retry, persistent store or implicit new-session fallback.
type Client struct {
    CodexBinary string
    Webhook     Webhook
}

func (c Client) sender(d Destination) Sender {
    switch d.Kind {
    case "codex":
        return Codex{Binary: c.CodexBinary}
    case "claude-channel":
        return ClaudeChannel{}
    case "webhook":
        return c.Webhook
    }
    return nil
}

// Probe reports only capabilities supplied by the explicitly selected adapter.
func (c Client) Probe(ctx context.Context, d Destination) (Capabilities, error) {
    sender := c.sender(d)
    if sender == nil {
        return Capabilities{ReasonCode: "DESTINATION_UNSUPPORTED"}, ErrUnsupported
    }
    return sender.Probe(ctx, d)
}

// Deliver attempts one send without inferring a retry policy.
func (c Client) Deliver(ctx context.Context, d Destination, m Message) (Receipt, error) {
    sender := c.sender(d)
    if sender == nil {
        return Receipt{MessageID: m.ID, State: "rejected", ReasonCode: "DESTINATION_UNSUPPORTED"}, ErrUnsupported
    }
    return sender.Deliver(ctx, d, m)
}

// Lookup uses explicit acknowledgement evidence when the adapter provides it.
func (c Client) Lookup(ctx context.Context, d Destination, receipt Receipt) (Receipt, error) {
    if querier, ok := c.sender(d).(ReceiptQuerier); ok {
        return querier.Lookup(ctx, d, receipt)
    }
    return receipt, ErrUnsupported
}
