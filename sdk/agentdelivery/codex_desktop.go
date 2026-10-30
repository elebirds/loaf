package agentdelivery

import (
    "context"
    "crypto/rand"
    "encoding/binary"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "net"
    "time"
)

// CodexDesktop addresses the existing owner of one desktop thread. Its peer IPC
// protocol is experimental and separate from the public App Server transport.
// It never starts a server, resumes a thread, or changes the thread's settings.
type CodexDesktop struct{}

const desktopFrameLimit = 1 << 20

type desktopIPC struct {
    connection net.Conn
    clientID   string
}

type desktopResponse struct {
    Type       string          `json:"type"`
    RequestID  string          `json:"requestId"`
    Method     string          `json:"method"`
    ResultType string          `json:"resultType"`
    HandledBy  string          `json:"handledByClientId"`
    Result     json.RawMessage `json:"result"`
}

func openDesktopIPC(ctx context.Context, d Destination) (*desktopIPC, error) {
    if d.Validate() != nil || d.Kind != "codex-desktop" {
        return nil, ErrDestination
    }
    if err := privateDesktopSocket(d.Endpoint); err != nil {
        return nil, err
    }
    connection, err := (&net.Dialer{}).DialContext(ctx, "unix", d.Endpoint)
    if err != nil {
        return nil, ErrUnavailable
    }
    client := &desktopIPC{connection: connection}
    result, err := client.call(ctx, "initialize", 0, map[string]any{"clientType": "agentdelivery"}, "")
    initialized := struct {
        ClientID string `json:"clientId"`
    }{}
    if err != nil || json.Unmarshal(result.Result, &initialized) != nil || !validID(initialized.ClientID) {
        connection.Close()
        return nil, ErrUnavailable
    }
    client.clientID = initialized.ClientID
    return client, nil
}

func (c *desktopIPC) write(value any) error {
    encoded, err := json.Marshal(value)
    if err != nil || len(encoded) > desktopFrameLimit {
        return ErrDestination
    }
    frame := make([]byte, 4+len(encoded))
    binary.LittleEndian.PutUint32(frame, uint32(len(encoded)))
    copy(frame[4:], encoded)
    for len(frame) > 0 {
        n, err := c.connection.Write(frame)
        if err != nil {
            return err
        }
        if n == 0 {
            return io.ErrShortWrite
        }
        frame = frame[n:]
    }
    return nil
}

func (c *desktopIPC) call(ctx context.Context, method string, version int, params any, target string) (desktopResponse, error) {
    var result desktopResponse
    if err := ctx.Err(); err != nil {
        return result, err
    }
    deadline := time.Now().Add(10 * time.Second)
    if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
        deadline = end
    }
    if err := c.connection.SetDeadline(deadline); err != nil {
        return result, err
    }
    stop := context.AfterFunc(ctx, func() { c.connection.Close() })
    defer stop()
    var random [16]byte
    if _, err := rand.Read(random[:]); err != nil {
        return result, err
    }
    id := hex.EncodeToString(random[:])
    request := map[string]any{
        "type": "request", "requestId": id, "method": method,
        "params": params, "version": version, "timeoutMs": 5000,
    }
    if c.clientID != "" {
        request["sourceClientId"] = c.clientID
    }
    if target != "" {
        request["targetClientId"] = target
    }
    if err := c.write(request); err != nil {
        return result, err
    }
    for {
        var header [4]byte
        if _, err := io.ReadFull(c.connection, header[:]); err != nil {
            return result, err
        }
        size := binary.LittleEndian.Uint32(header[:])
        if size == 0 || size > 16<<20 {
            return result, ErrUnavailable
        }
        // The shared bus can carry other threads' snapshots. Discard oversized
        // unsolicited frames without retaining their content or allocating it.
        if size > desktopFrameLimit {
            if _, err := io.CopyN(io.Discard, c.connection, int64(size)); err != nil {
                return result, err
            }
            continue
        }
        encoded := make([]byte, size)
        if _, err := io.ReadFull(c.connection, encoded); err != nil {
            return result, err
        }
        result = desktopResponse{}
        if err := json.Unmarshal(encoded, &result); err != nil {
            return result, ErrUnavailable
        }
        if result.Type == "client-discovery-request" {
            if err := c.write(map[string]any{
                "type": "client-discovery-response", "requestId": result.RequestID,
                "response": map[string]bool{"canHandle": false},
            }); err != nil {
                return result, err
            }
            continue
        }
        if result.Type != "response" || result.RequestID != id {
            continue
        }
        if result.ResultType != "success" || result.Method != method || target != "" && result.HandledBy != target {
            return result, ErrUnavailable
        }
        return result, nil
    }
}

func (c *desktopIPC) owner(ctx context.Context, d Destination) (string, error) {
    response, err := c.call(ctx, "thread-owner-discovery", 1, map[string]string{
        "hostId": "local", "conversationId": d.SessionID,
    }, "")
    capability := struct {
        Untrusted bool `json:"supportsUntrustedAppInput"`
    }{}
    if err != nil || !validID(response.HandledBy) || json.Unmarshal(response.Result, &capability) != nil || !capability.Untrusted {
        return "", ErrUnavailable
    }
    return response.HandledBy, nil
}

// Probe discovers the existing owner of the exact thread without model work.
func (CodexDesktop) Probe(ctx context.Context, d Destination) (Capabilities, error) {
    c, err := openDesktopIPC(ctx, d)
    if err != nil {
        if errors.Is(err, ErrUnsupported) {
            return Capabilities{Experimental: true, ReasonCode: "DESTINATION_UNSUPPORTED"}, err
        }
        return Capabilities{Experimental: true, ReasonCode: "CODEX_DESKTOP_UNAVAILABLE"}, err
    }
    defer c.connection.Close()
    if _, err := c.owner(ctx, d); err != nil {
        return Capabilities{Experimental: true, ReasonCode: "CODEX_DESKTOP_THREAD_UNAVAILABLE"}, err
    }
    return Capabilities{Experimental: true, ExistingSession: true, Available: true}, nil
}

// Deliver supplies one standalone tool output to the existing desktop owner.
// No model, cwd, permissions, sandbox, or active-turn steer is supplied.
func (CodexDesktop) Deliver(ctx context.Context, d Destination, m Message) (Receipt, error) {
    receipt := Receipt{MessageID: m.ID, State: "rejected", RetrySafe: true}
    if m.Validate() != nil {
        receipt.RetrySafe = false
        return receipt, ErrMessage
    }
    c, err := openDesktopIPC(ctx, d)
    if err != nil {
        receipt.ReasonCode = "CODEX_DESKTOP_UNAVAILABLE"
        receipt.RetrySafe = !errors.Is(err, ErrDestination)
        if errors.Is(err, ErrUnsupported) {
            receipt.ReasonCode, receipt.RetrySafe = "DESTINATION_UNSUPPORTED", false
        }
        return receipt, err
    }
    defer c.connection.Close()
    owner, err := c.owner(ctx, d)
    if err != nil {
        receipt.ReasonCode = "CODEX_DESKTOP_THREAD_UNAVAILABLE"
        return receipt, err
    }
    if err := ctx.Err(); err != nil {
        return receipt, err
    }
    body, _ := json.Marshal(m)
    request := map[string]any{
        "threadId": d.SessionID, "input": []any{}, "clientUserMessageId": m.ID,
        "toolOutput": map[string]string{"name": "external_event", "namespace": "agentdelivery", "output": string(body)},
    }
    receipt.State, receipt.RetrySafe = "unconfirmed", false
    response, err := c.call(ctx, "thread-follower-start-turn", 2, map[string]any{
        "conversationId": d.SessionID,
        "turnStart":      map[string]any{"request": request, "context": map[string]bool{"inheritThreadSettings": true}},
    }, owner)
    result := struct {
        Result struct {
            Turn struct {
                ID string `json:"id"`
            } `json:"turn"`
        } `json:"result"`
    }{}
    if err != nil || json.Unmarshal(response.Result, &result) != nil || result.Result.Turn.ID == "" {
        receipt.ReasonCode = "CODEX_DESKTOP_SEND_UNCONFIRMED"
        return receipt, ErrUnconfirmed
    }
    receipt.State, receipt.OperationID = "accepted", result.Result.Turn.ID
    return receipt, nil
}
