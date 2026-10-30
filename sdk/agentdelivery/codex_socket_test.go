//go:build darwin || linux

package agentdelivery

import (
    "context"
    "encoding/json"
    "net"
    "net/http"
    "os"
    "path/filepath"
    "sync/atomic"
    "testing"
    "time"

    "github.com/coder/websocket"
)

func TestCodexPublicUnixWebSocketTransport(t *testing.T) {
    directory, err := os.MkdirTemp("/tmp", "ad-codex-")
    if err != nil {
        t.Fatal(err)
    }
    defer os.RemoveAll(directory)
    socket := filepath.Join(directory, "socket")
    listener, err := net.Listen("unix", socket)
    if err != nil {
        t.Fatal(err)
    }
    defer listener.Close()
    if err = os.Chmod(socket, 0600); err != nil {
        t.Fatal(err)
    }
    var sends atomic.Int32
    server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        connection, err := websocket.Accept(w, r, nil)
        if err != nil {
            return
        }
        defer connection.CloseNow()
        for {
            _, data, err := connection.Read(context.Background())
            if err != nil {
                return
            }
            var req struct {
                ID     json.RawMessage `json:"id"`
                Method string          `json:"method"`
            }
            if json.Unmarshal(data, &req) != nil {
                return
            }
            if req.Method == "initialized" {
                continue
            }
            var result any = map[string]any{}
            switch req.Method {
            case "thread/read":
                result = map[string]any{"thread": map[string]any{"id": "thread_1", "status": map[string]string{"type": "idle"}}}
            case "turn/start":
                sends.Add(1)
                result = map[string]any{"turn": map[string]string{"id": "turn_1"}}
            }
            response, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
            if err = connection.Write(context.Background(), websocket.MessageText, response); err != nil {
                return
            }
        }
    })}
    defer server.Close()
    go server.Serve(listener)
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    destination := Destination{Kind: "codex", Endpoint: socket, SessionID: "thread_1"}
    receipt, err := (Codex{}).Deliver(ctx, destination, Message{ID: "message_1", Text: "File processing complete"})
    if err != nil || receipt.State != "accepted" || receipt.OperationID != "turn_1" || sends.Load() != 1 {
        t.Fatal(receipt, err, sends.Load())
    }
    if err = os.Chmod(socket, 0666); err != nil {
        t.Fatal(err)
    }
    if _, err = (Codex{}).Probe(ctx, destination); err == nil {
        t.Fatal("public socket permissions accepted")
    }
}
