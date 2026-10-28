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
    "sync/atomic"
    "testing"
    "time"
)

func desktopFixture(t *testing.T, lost, supported bool) (Destination, *atomic.Int32) {
    t.Helper()
    directory, err := os.MkdirTemp("/tmp", "ad-desktop-")
    if err != nil {
        t.Fatal(err)
    }
    path := filepath.Join(directory, "ipc.sock")
    listener, err := net.Listen("unix", path)
    if err != nil {
        os.RemoveAll(directory)
        t.Fatal(err)
    }
    if err := os.Chmod(path, 0600); err != nil {
        t.Fatal(err)
    }
    sends := &atomic.Int32{}
    done := make(chan struct{})
    go func() {
        defer close(done)
        for {
            connection, err := listener.Accept()
            if err != nil {
                return
            }
            func() {
                defer connection.Close()
                connection.SetDeadline(time.Now().Add(3 * time.Second))
                for {
                    var header [4]byte
                    if _, err := io.ReadFull(connection, header[:]); err != nil {
                        return
                    }
                    size := binary.LittleEndian.Uint32(header[:])
                    if size == 0 || size > desktopFrameLimit {
                        t.Error("invalid outgoing frame", size)
                        return
                    }
                    body := make([]byte, size)
                    if _, err := io.ReadFull(connection, body); err != nil {
                        return
                    }
                    var request struct {
                        ID      string          `json:"requestId"`
                        Method  string          `json:"method"`
                        Version int             `json:"version"`
                        Source  string          `json:"sourceClientId"`
                        Target  string          `json:"targetClientId"`
                        Params  json.RawMessage `json:"params"`
                    }
                    if json.Unmarshal(body, &request) != nil {
                        t.Error("invalid JSON")
                        return
                    }
                    result := any(map[string]any{})
                    resultType := "success"
                    switch request.Method {
                    case "initialize":
                        if string(request.Params) != `{"clientType":"agentdelivery"}` || request.Version != 0 {
                            t.Error("wrong sender identity", string(body))
                        }
                        result = map[string]string{"clientId": "delivery-client"}
                    case "thread-owner-discovery":
                        var params map[string]string
                        json.Unmarshal(request.Params, &params)
                        if params["hostId"] != "local" || request.Version != 1 || request.Source != "delivery-client" {
                            t.Error("unbound discovery", string(body))
                        }
                        result = map[string]bool{"supportsUntrustedAppInput": supported}
                        if params["conversationId"] != "thread_1" {
                            resultType = "error"
                        }
                    case "thread-follower-start-turn":
                        sends.Add(1)
                        var params struct {
                            Conversation string `json:"conversationId"`
                            TurnStart    struct {
                                Request map[string]json.RawMessage `json:"request"`
                                Context map[string]bool            `json:"context"`
                            }   `json:"turnStart"`
                        }
                        if json.Unmarshal(request.Params, &params) != nil || request.Version != 2 || request.Source != "delivery-client" || request.Target != "owner_1" || params.Conversation != "thread_1" {
                            t.Error("wrong owner or thread", string(body))
                            return
                        }
                        input := params.TurnStart.Request
                        if len(input) != 4 || string(input["threadId"]) != `"thread_1"` || string(input["input"]) != "[]" || string(input["clientUserMessageId"]) != `"event_1"` || len(input["toolOutput"]) == 0 || !params.TurnStart.Context["inheritThreadSettings"] || len(params.TurnStart.Context) != 1 {
                            t.Error("delivery altered user input, workspace, or policy", string(body))
                            return
                        }
                        if lost {
                            return
                        }
                        result = map[string]any{"result": map[string]any{"turn": map[string]string{"id": "turn_1"}}}
                    default:
                        t.Error("unexpected desktop operation", request.Method)
                        return
                    }
                    wire := &desktopIPC{connection: connection}
                    if err := wire.write(map[string]any{"type": "broadcast", "method": "client-status-changed", "requestId": "other"}); err != nil {
                        return
                    }
                    if err := wire.write(map[string]any{"type": "response", "requestId": request.ID, "method": request.Method, "resultType": resultType, "handledByClientId": "owner_1", "result": result}); err != nil {
                        return
                    }
                }
            }()
        }
    }()
    t.Cleanup(func() { listener.Close(); <-done; os.RemoveAll(directory) })
    return Destination{Kind: "codex-desktop", Endpoint: path, SessionID: "thread_1"}, sends
}

func TestCodexDesktopExactCurrentOwnerAndToolOutput(t *testing.T) {
    destination, sends := desktopFixture(t, false, true)
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    capabilities, err := (Client{}).Probe(ctx, destination)
    if err != nil || !capabilities.Available || !capabilities.ExistingSession || sends.Load() != 0 {
        t.Fatal(capabilities, err, sends.Load())
    }
    receipt, err := (Client{}).Deliver(ctx, destination, Message{ID: "event_1", Text: "File processing complete"})
    if err != nil || receipt.State != "accepted" || receipt.OperationID != "turn_1" || receipt.RetrySafe || sends.Load() != 1 {
        t.Fatal(receipt, err, sends.Load())
    }
}

func TestCodexDesktopRefusesUnknownThreadOrLegacyOwner(t *testing.T) {
    for _, supported := range []bool{false, true} {
        destination, sends := desktopFixture(t, false, supported)
        if supported {
            destination.SessionID = "different_thread"
        }
        receipt, err := (CodexDesktop{}).Deliver(context.Background(), destination, Message{ID: "event_1", Text: "Finished"})
        if err == nil || receipt.State != "rejected" || sends.Load() != 0 {
            t.Fatal(receipt, err, sends.Load())
        }
    }
}

func TestCodexDesktopLostConfirmationNeverReplays(t *testing.T) {
    destination, sends := desktopFixture(t, true, true)
    receipt, err := (CodexDesktop{}).Deliver(context.Background(), destination, Message{ID: "event_1", Text: "Finished"})
    if err != ErrUnconfirmed || receipt.State != "unconfirmed" || receipt.RetrySafe || sends.Load() != 1 {
        t.Fatal(receipt, err, sends.Load())
    }
}

func TestCodexDesktopRejectsUnsafeSocketAndWorkspaceOverride(t *testing.T) {
    destination, sends := desktopFixture(t, false, true)
    for _, unsafe := range []string{"socket", "directory", "workspace", "symlink"} {
        t.Run(unsafe, func(t *testing.T) {
            address := destination
            switch unsafe {
            case "socket":
                os.Chmod(destination.Endpoint, 0666)
                defer os.Chmod(destination.Endpoint, 0600)
            case "directory":
                os.Chmod(filepath.Dir(destination.Endpoint), 0755)
                defer os.Chmod(filepath.Dir(destination.Endpoint), 0700)
            case "workspace":
                address.CWD = "/another-workspace"
            case "symlink":
                address.Endpoint += "-link"
                if err := os.Symlink(destination.Endpoint, address.Endpoint); err != nil {
                    t.Fatal(err)
                }
            }
            if _, err := (CodexDesktop{}).Probe(context.Background(), address); err == nil || sends.Load() != 0 {
                t.Fatal("unsafe binding accepted", unsafe, err)
            }
        })
    }
}
