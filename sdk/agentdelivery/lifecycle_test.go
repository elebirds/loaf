//go:build darwin || linux

package agentdelivery

import (
    "context"
    "encoding/binary"
    "encoding/json"
    "io"
    "net"
    "os"
    "path/filepath"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    "github.com/modelcontextprotocol/go-sdk/jsonrpc"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func startLifecycleChannel(t *testing.T, socket string) (Destination, mcp.Connection, func()) {
    t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    server, peer := net.Pipe()
    done := make(chan struct{})
    var serverErr error
    go func() {
        defer close(done)
        defer server.Close()
        serverErr = RunClaudeChannel(ctx, socket, "session_1", server, server)
    }()
    transport := &mcp.IOTransport{Reader: peer, Writer: peer}
    connection, err := transport.Connect(ctx)
    if err != nil {
        cancel()
        server.Close()
        peer.Close()
        t.Fatal(err)
    }
    var once sync.Once
    stop := func() {
        once.Do(func() {
            cancel()
            connection.Close()
            peer.Close()
            <-done
        })
    }
    t.Cleanup(stop)
    ready := time.NewTicker(time.Millisecond)
    defer ready.Stop()
    for {
        if info, err := os.Lstat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
            break
        }
        select {
        case <-done:
            t.Fatal("Channel exited before opening its socket", serverErr)
        case <-ctx.Done():
            t.Fatal(ctx.Err())
        case <-ready.C:
        }
    }
    id, _ := jsonrpc.MakeID("initialize")
    if err := connection.Write(ctx, &jsonrpc.Request{ID: id, Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"lifecycle-fixture","version":"1"}}`)}); err != nil {
        t.Fatal(err)
    }
    if _, err := connection.Read(ctx); err != nil {
        t.Fatal(err)
    }
    if err := connection.Write(ctx, &jsonrpc.Request{Method: "notifications/initialized"}); err != nil {
        t.Fatal(err)
    }
    destination := Destination{Kind: "claude-channel", Endpoint: socket, SessionID: "session_1"}
    ticker := time.NewTicker(time.Millisecond)
    defer ticker.Stop()
    for {
        if capabilities, err := (ClaudeChannel{}).Probe(ctx, destination); err == nil && capabilities.Available {
            return destination, connection, stop
        }
        select {
        case <-ctx.Done():
            t.Fatal("Channel did not become ready", ctx.Err())
        case <-ticker.C:
        }
    }
}

func TestClaudeHelperRestartLosesACKCacheWithoutReplayPermission(t *testing.T) {
    directory, err := os.MkdirTemp("/tmp", "ad-restart-")
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { os.RemoveAll(directory) })
    destination, connection, stop := startLifecycleChannel(t, filepath.Join(directory, "socket"))
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    sent := make(chan Receipt, 1)
    go func() {
        receipt, err := (ClaudeChannel{}).Deliver(ctx, destination, Message{ID: "before_restart", Text: "An isolated fact"})
        if err != nil {
            t.Error(err)
        }
        sent <- receipt
    }()
    message, err := connection.Read(ctx)
    notification, ok := message.(*jsonrpc.Request)
    if err != nil || !ok || notification.Method != "notifications/claude/channel" {
        t.Fatal("missing original notification", message, err)
    }
    original := <-sent
    if original.State != "written" || original.RetrySafe {
        t.Fatal(original)
    }
    stop()
    if _, err := (ClaudeChannel{}).Probe(ctx, destination); err == nil {
        t.Fatal("closed helper remained available")
    }
    restarted, _, _ := startLifecycleChannel(t, destination.Endpoint)
    for range 2 {
        receipt, err := (ClaudeChannel{}).Lookup(ctx, restarted, original)
        if err != nil || receipt.State != "unconfirmed" || receipt.Accepted() || receipt.RetrySafe || receipt.ReasonCode != "CLAUDE_RECEIPT_UNAVAILABLE" {
            t.Fatal("missing ACK cache became a new send or acceptance", receipt, err)
        }
    }
}

func TestCodexClosedDesktopOwnerDoesNotSelectAnotherEndpoint(t *testing.T) {
    var closed Destination
    t.Run("original-owner", func(t *testing.T) {
        closed, _ = desktopFixture(t, false, true)
        if _, err := (CodexDesktop{}).Probe(context.Background(), closed); err != nil {
            t.Fatal(err)
        }
    })
    _, substituteSends := desktopFixture(t, false, true)
    receipt, err := (Client{}).Deliver(context.Background(), closed, Message{ID: "event_1", Text: "Keep the original owner"})
    if err == nil || receipt.State != "rejected" || substituteSends.Load() != 0 {
        t.Fatal("closed owner selected a substitute", receipt, err, substituteSends.Load())
    }
}

// A response from a different owner cannot confirm the attempted turn. The
// SDK must retain uncertainty rather than discovering a replacement and retrying.
func TestCodexDesktopChangedOwnerCannotConfirmOrReplayTurn(t *testing.T) {
    directory, err := os.MkdirTemp("/tmp", "ad-owner-")
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { os.RemoveAll(directory) })
    socket := filepath.Join(directory, "socket")
    listener, err := net.Listen("unix", socket)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.Chmod(socket, 0600); err != nil {
        t.Fatal(err)
    }
    var sends atomic.Int32
    done := make(chan struct{})
    go func() {
        defer close(done)
        connection, err := listener.Accept()
        if err != nil {
            return
        }
        defer connection.Close()
        _ = connection.SetDeadline(time.Now().Add(5 * time.Second))
        for {
            var header [4]byte
            if _, err := io.ReadFull(connection, header[:]); err != nil {
                return
            }
            size := binary.LittleEndian.Uint32(header[:])
            if size == 0 || size > desktopFrameLimit {
                return
            }
            body := make([]byte, size)
            if _, err := io.ReadFull(connection, body); err != nil {
                return
            }
            var request struct {
                ID     string `json:"requestId"`
                Method string `json:"method"`
            }
            if err := json.Unmarshal(body, &request); err != nil {
                return
            }
            owner := "owner_original"
            var result any
            switch request.Method {
            case "initialize":
                result = map[string]string{"clientId": "fixture_sender"}
            case "thread-owner-discovery":
                result = map[string]bool{"supportsUntrustedAppInput": true}
            case "thread-follower-start-turn":
                sends.Add(1)
                owner = "owner_replacement"
                result = map[string]any{"result": map[string]any{"turn": map[string]string{"id": "wrong_owner_turn"}}}
            default:
                t.Error("unexpected operation", request.Method)
                return
            }
            wire := desktopIPC{connection: connection}
            if err := wire.write(map[string]any{"type": "response", "requestId": request.ID, "method": request.Method, "resultType": "success", "handledByClientId": owner, "result": result}); err != nil {
                return
            }
        }
    }()
    t.Cleanup(func() { listener.Close(); <-done })
    destination := Destination{Kind: "codex-desktop", Endpoint: socket, SessionID: "original_thread"}
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    receipt, err := (CodexDesktop{}).Deliver(ctx, destination, Message{ID: "event_1", Text: "One owner only"})
    if err != ErrUnconfirmed || receipt.State != "unconfirmed" || receipt.RetrySafe || receipt.OperationID != "" || sends.Load() != 1 {
        t.Fatal("changed owner forged acceptance or caused replay", receipt, err, sends.Load())
    }
}
