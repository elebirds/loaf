package agentdelivery

import (
    "context"
    "encoding/json"
    "path/filepath"

    "github.com/pmenglund/codex-sdk-go/rpc"
)

// Codex delivers standalone tool results to an explicitly selected live thread.
// It connects to a private Unix WebSocket on an already running app-server; it never
// starts an app-server, creates a thread, edits transcripts or overrides policy.
type Codex struct {
    open func(context.Context, Destination) (rpc.Transport, error)
}

func (c Codex) connect(ctx context.Context, d Destination) (*rpc.Client, error) {
    if err := d.Validate(); err != nil || d.Kind != "codex" {
        return nil, ErrDestination
    }
    if d.Endpoint != "" && !filepath.IsAbs(d.Endpoint) {
        return nil, ErrDestination
    }
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    var transport rpc.Transport
    var err error
    if c.open != nil {
        transport, err = c.open(ctx, d)
    } else {
        transport, err = openCodexSocket(ctx, d.Endpoint)
    }
    if err != nil {
        return nil, ErrUnavailable
    }
    client, err := rpc.NewClientChecked(transport, rpc.ClientOptions{MaxMessageBytes: 1 << 20})
    if err != nil {
        transport.Close()
        return nil, ErrUnavailable
    }
    var initialized json.RawMessage
    err = client.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "agent-delivery", "title": "Agent delivery", "version": "1"}}, &initialized)
    if err == nil {
        err = client.Notify(ctx, "initialized", map[string]any{})
    }
    if err != nil {
        client.Close()
        return nil, ErrUnavailable
    }
    return client, nil
}

func codexThread(ctx context.Context, c *rpc.Client, d Destination) (Capabilities, error) {
    result := struct {
        Thread struct {
            ID     string `json:"id"`
            CWD    string `json:"cwd"`
            Status struct {
                Type string `json:"type"`
            }   `json:"status"`
        } `json:"thread"`
    }{}
    if err := c.Call(ctx, "thread/read", map[string]any{"threadId": d.SessionID, "includeTurns": false}, &result); err != nil {
        return Capabilities{ExistingSession: true, ReasonCode: "CODEX_THREAD_UNAVAILABLE"}, ErrUnavailable
    }
    if result.Thread.ID != d.SessionID {
        return Capabilities{ReasonCode: "DESTINATION_ID_MISMATCH"}, ErrDestination
    }
    if d.CWD != "" && filepath.Clean(d.CWD) != filepath.Clean(result.Thread.CWD) {
        return Capabilities{ReasonCode: "DESTINATION_WORKSPACE_MISMATCH"}, ErrDestination
    }
    if result.Thread.Status.Type != "idle" && result.Thread.Status.Type != "active" {
        return Capabilities{ExistingSession: true, ReasonCode: "CODEX_THREAD_NOT_LOADED"}, ErrUnavailable
    }
    return Capabilities{ExistingSession: true, Available: true}, nil
}

// Probe reads explicit thread metadata without starting a model turn.
func (c Codex) Probe(ctx context.Context, d Destination) (Capabilities, error) {
    client, err := c.connect(ctx, d)
    if err != nil {
        return Capabilities{ReasonCode: "CODEX_UNAVAILABLE"}, err
    }
    defer client.Close()
    return codexThread(ctx, client, d)
}

// Deliver uses the documented standalone toolOutput path. An active regular
// turn queues this result rather than being steered or interrupted.
func (c Codex) Deliver(ctx context.Context, d Destination, m Message) (Receipt, error) {
    receipt := Receipt{MessageID: m.ID, State: "rejected", RetrySafe: true}
    if m.Validate() != nil {
        receipt.RetrySafe = false
        return receipt, ErrMessage
    }
    client, err := c.connect(ctx, d)
    if err != nil {
        if err == ErrDestination {
            receipt.RetrySafe = false
        }
        receipt.ReasonCode = "CODEX_UNAVAILABLE"
        return receipt, err
    }
    defer client.Close()
    if _, err = codexThread(ctx, client, d); err != nil {
        if err == ErrDestination {
            receipt.RetrySafe = false
        }
        receipt.ReasonCode = "CODEX_THREAD_UNAVAILABLE"
        return receipt, err
    }
    if err = ctx.Err(); err != nil {
        return receipt, err
    }
    body, _ := json.Marshal(m)
    params := map[string]any{
        "threadId": d.SessionID, "input": []any{},
        "toolOutput":          map[string]any{"name": "external_event", "namespace": "agentdelivery", "output": string(body)},
        "clientUserMessageId": m.ID,
    }
    result := struct {
        Turn struct {
            ID     string `json:"id"`
            Status string `json:"status"`
        } `json:"turn"`
    }{}
    receipt.State, receipt.RetrySafe = "unconfirmed", false
    if err = client.Call(ctx, "turn/start", params, &result); err != nil {
        receipt.ReasonCode = "CODEX_SEND_UNCONFIRMED"
        return receipt, ErrUnconfirmed
    }
    if result.Turn.ID == "" {
        receipt.ReasonCode = "CODEX_RECEIPT_INVALID"
        return receipt, ErrUnconfirmed
    }
    receipt.State, receipt.OperationID, receipt.ReasonCode = "accepted", result.Turn.ID, ""
    return receipt, nil
}
