package agentdelivery

import (
    "context"
    "encoding/json"
    "net"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/modelcontextprotocol/go-sdk/jsonrpc"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestClaudeChannelWriteAndExplicitAgentAcknowledgement(t *testing.T) {
    directory, err := os.MkdirTemp("/tmp", "agent-delivery-channel-")
    if err != nil {
        t.Fatal(err)
    }
    defer os.RemoveAll(directory)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    serverConnection, clientConnection := net.Pipe()
    defer serverConnection.Close()
    defer clientConnection.Close()
    clientTransport := &mcp.IOTransport{Reader: clientConnection, Writer: clientConnection}
    channel := &ClaudeChannelServer{SocketPath: filepath.Join(directory, "socket"), SessionID: "session_1"}
    finished := make(chan error, 1)
    go func() {
        finished <- RunClaudeChannel(ctx, channel.SocketPath, channel.SessionID, serverConnection, serverConnection)
    }()
    connection, err := clientTransport.Connect(ctx)
    if err != nil {
        t.Fatal(err)
    }
    defer connection.Close()
    id, _ := jsonrpc.MakeID("1")
    params := json.RawMessage(`{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}`)
    if err = connection.Write(ctx, &jsonrpc.Request{ID: id, Method: "initialize", Params: params}); err != nil {
        t.Fatal(err)
    }
    response, err := connection.Read(ctx)
    if err != nil {
        t.Fatal(err)
    }
    initialized, ok := response.(*jsonrpc.Response)
    if !ok || initialized.Error != nil {
        t.Fatal(response)
    }
    var handshake struct {
        ProtocolVersion string `json:"protocolVersion"`
    }
    if json.Unmarshal(initialized.Result, &handshake) != nil || handshake.ProtocolVersion != "2025-11-25" {
        t.Fatal("channel negotiated incompatible version", string(initialized.Result))
    }
    if err = connection.Write(ctx, &jsonrpc.Request{Method: "notifications/initialized"}); err != nil {
        t.Fatal(err)
    }
    destination := Destination{Kind: "claude-channel", Endpoint: channel.SocketPath, SessionID: "session_1"}
    adapter := ClaudeChannel{}
    ready := time.NewTicker(time.Millisecond)
    defer ready.Stop()
    for {
        if capabilities, err := adapter.Probe(ctx, destination); err == nil && capabilities.Available {
            break
        }
        select {
        case <-ctx.Done():
            t.Fatal(ctx.Err())
        case <-ready.C:
        }
    }
    type delivered struct {
        receipt Receipt
        err     error
    }
    delivery := make(chan delivered, 1)
    go func() {
        receipt, err := adapter.Deliver(ctx, destination, Message{ID: "event_1", Subject: "finished", Text: "Finished"})
        delivery <- delivered{receipt, err}
    }()
    notification, err := connection.Read(ctx)
    if err != nil {
        t.Fatal(err)
    }
    note, ok := notification.(*jsonrpc.Request)
    if !ok || note.Method != "notifications/claude/channel" {
        t.Fatal(notification)
    }
    var payload struct {
        Meta map[string]string `json:"meta"`
    }
    json.Unmarshal(note.Params, &payload)
    if payload.Meta["message_id"] != "event_1" {
        t.Fatal("notification cannot be correlated", payload)
    }
    sent := <-delivery
    receipt, err := sent.receipt, sent.err
    if err != nil || receipt.State != "written" || receipt.Accepted() {
        t.Fatal("transport write became processing", receipt, err)
    }
    lookup, err := adapter.Lookup(ctx, destination, receipt)
    if err != nil || lookup.State != "written" {
        t.Fatal(lookup, err)
    }
    id, _ = jsonrpc.MakeID("2")
    if err = connection.Write(ctx, &jsonrpc.Request{ID: id, Method: "tools/call", Params: json.RawMessage(`{"name":"ack_event","arguments":{"message_id":"event_1"}}`)}); err != nil {
        t.Fatal(err)
    }
    if _, err = connection.Read(ctx); err != nil {
        t.Fatal(err)
    }
    lookup, err = adapter.Lookup(ctx, destination, receipt)
    if err != nil || lookup.State != "processed" {
        t.Fatal("explicit acknowledgement lost", lookup, err)
    }
    // The same live helper recognizes a duplicate without a second notification.
    duplicate, err := adapter.Deliver(ctx, destination, Message{ID: "event_1", Subject: "finished", Text: "Finished"})
    if err != nil || duplicate.State != "processed" {
        t.Fatal(duplicate, err)
    }
    conflict, err := adapter.Deliver(ctx, destination, Message{ID: "event_1", Subject: "finished", Text: "Different fact"})
    if err != nil || conflict.State != "rejected" || conflict.ReasonCode != "MESSAGE_ID_CONFLICT" || conflict.RetrySafe {
        t.Fatal("changed message reused receipt", conflict, err)
    }
    cancel()
    connection.Close()
    select {
    case <-finished:
    case <-time.After(time.Second):
        t.Fatal("channel did not close its owned connections")
    }
}
