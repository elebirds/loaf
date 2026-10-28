package agentdelivery

import (
    "bufio"
    "context"
    "encoding/json"
    "net"
    "sync/atomic"
    "testing"
    "time"

    "github.com/pmenglund/codex-sdk-go/rpc"
)

func codexFixture(t *testing.T, lost bool, threadID string) (Codex, *atomic.Int32) {
    t.Helper()
    calls := &atomic.Int32{}
    adapter := Codex{open: func(context.Context, Destination) (rpc.Transport, error) {
        client, server := net.Pipe()
        go func() {
            defer server.Close()
            scanner := bufio.NewScanner(server)
            for scanner.Scan() {
                var request struct {
                    ID     json.RawMessage `json:"id"`
                    Method string          `json:"method"`
                    Params json.RawMessage `json:"params"`
                }
                if json.Unmarshal(scanner.Bytes(), &request) != nil {
                    return
                }
                if request.Method == "initialized" {
                    continue
                }
                var result any = map[string]any{}
                switch request.Method {
                case "thread/read":
                    result = map[string]any{"thread": map[string]any{"id": threadID, "cwd": "/work", "status": map[string]string{"type": "active"}}}
                case "turn/start":
                    calls.Add(1)
                    var params map[string]json.RawMessage
                    if json.Unmarshal(request.Params, &params) != nil || string(params["input"]) != "[]" || len(params["toolOutput"]) == 0 || len(params["sandboxPolicy"]) != 0 || len(params["approvalPolicy"]) != 0 {
                        t.Error("delivery changed permissions or ordinary user input", string(request.Params))
                        return
                    }
                    if lost {
                        return
                    }
                    result = map[string]any{"turn": map[string]string{"id": "turn_1", "status": "inProgress"}}
                default:
                    if request.Method != "initialize" {
                        t.Error("unexpected method", request.Method)
                        return
                    }
                }
                if json.NewEncoder(server).Encode(map[string]any{"id": request.ID, "result": result}) != nil {
                    return
                }
            }
        }()
        return rpc.NewConnTransportChecked(client)
    }}
    return adapter, calls
}

func TestCodexExplicitThreadToolOutputReceipt(t *testing.T) {
    adapter, calls := codexFixture(t, false, "thread_1")
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    receipt, err := adapter.Deliver(ctx, Destination{Kind: "codex", SessionID: "thread_1", CWD: "/work"}, Message{ID: "event_1", Subject: "finished", Text: "File processing finished"})
    if err != nil || !receipt.Accepted() || receipt.State != "accepted" || receipt.OperationID != "turn_1" || receipt.RetrySafe || calls.Load() != 1 {
        t.Fatal(receipt, err, calls.Load())
    }
}

func TestCodexLostConfirmationNeverAutomaticallyReplays(t *testing.T) {
    adapter, calls := codexFixture(t, true, "thread_1")
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    receipt, err := adapter.Deliver(ctx, Destination{Kind: "codex", SessionID: "thread_1"}, Message{ID: "event_1", Text: "Finished"})
    if err != ErrUnconfirmed || receipt.State != "unconfirmed" || receipt.RetrySafe || calls.Load() != 1 {
        t.Fatal(receipt, err, calls.Load())
    }
}

func TestCodexRefusesWrongExistingThreadBeforeSideEffect(t *testing.T) {
    adapter, calls := codexFixture(t, false, "other_thread")
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    receipt, err := adapter.Deliver(ctx, Destination{Kind: "codex", SessionID: "thread_1"}, Message{ID: "event_1", Text: "Finished"})
    if err != ErrDestination || receipt.State != "rejected" || calls.Load() != 0 {
        t.Fatal(receipt, err, calls.Load())
    }
}
