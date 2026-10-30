package agentdelivery

import (
    "context"
    "encoding/json"
    "errors"
    "net"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "runtime"
    "strings"
    "testing"
    "time"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestWebhookHTTPSBearerReferenceAndNoCredentialLeak(t *testing.T) {
    const token = "test-private-bearer-never-record"
    calls := 0
    server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        calls++
        if r.Header.Get("Authorization") != "Bearer "+token {
            t.Error("missing transient bearer")
        }
        w.WriteHeader(204)
    }))
    defer server.Close()
    dest := Destination{Kind: "webhook", Endpoint: server.URL, BearerRef: "HOOK_TOKEN"}
    receipt, err := (Webhook{Transport: server.Client().Transport, ResolveBearer: func(context.Context, string) (string, error) { return token, nil }}).Deliver(context.Background(), dest, Message{ID: "m_1", Text: "Finished"})
    data, _ := json.Marshal([]any{dest, receipt})
    if err != nil || receipt.State != "accepted" || calls != 1 || strings.Contains(string(data), token) {
        t.Fatal(receipt, err, calls, string(data))
    }
    receipt, err = (Webhook{Transport: server.Client().Transport, ResolveBearer: func(context.Context, string) (string, error) {
        return "", errors.New("private value should not escape")
    }}).Deliver(context.Background(), dest, Message{ID: "m_2", Text: "Finished"})
    if err != ErrUnavailable || receipt.State != "rejected" || !receipt.RetrySafe || calls != 1 || strings.Contains(err.Error(), "private value") {
        t.Fatal(receipt, err, calls)
    }
}
func TestWebhookHTTPPolicyAnd429AreNotAssumedSafe(t *testing.T) {
    for _, d := range []Destination{{Kind: "webhook", Endpoint: "http://receiver.example/events"}, {Kind: "webhook", Endpoint: "http://127.0.0.1/events", BearerRef: "TOKEN"}} {
        if d.Validate() == nil {
            t.Fatal("unsafe endpoint allowed", d)
        }
    }
    if (Destination{Kind: "webhook", Endpoint: "http://receiver.example/events", AllowInsecureHTTP: true}).Validate() != nil {
        t.Fatal("explicit HTTP policy rejected")
    }
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }))
    defer server.Close()
    r, err := (Webhook{}).Deliver(context.Background(), Destination{Kind: "webhook", Endpoint: server.URL}, Message{ID: "m_1", Text: "Done"})
    if err == nil || r.State != "rejected" || r.RetrySafe {
        t.Fatal("429 replay allowed", r, err)
    }
}
func TestCommonUnixSecurityAndPublicSymlinkException(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("Unix socket ownership; native Windows returns ErrUnsupported")
    }
    directory, err := os.MkdirTemp("/tmp", "ad-secure-")
    if err != nil {
        t.Fatal(err)
    }
    defer os.RemoveAll(directory)
    path := filepath.Join(directory, "socket")
    listener, err := net.Listen("unix", path)
    if err != nil {
        t.Fatal(err)
    }
    defer listener.Close()
    os.Chmod(path, 0600)
    link := filepath.Join(directory, "link")
    if err = os.Symlink(path, link); err != nil {
        t.Fatal(err)
    }
    if privateSocket(link, true) != nil || privateSocket(link, false) == nil {
        t.Fatal("symlink exception not explicit")
    }
    os.Chmod(directory, 0755)
    if privateSocket(path, true) == nil || privateSocket(path, false) == nil {
        t.Fatal("public parent accepted")
    }
    os.Chmod(directory, 0700)
}
func TestClaudeCapacityEvictsOnlyProcessedReceipts(t *testing.T) {
    s := &ClaudeChannelServer{receipts: map[string]Receipt{}, messages: map[string]Message{}}
    for i := 0; i < 256; i++ {
        id := string(rune('A' + i))
        s.order = append(s.order, id)
        s.receipts[id] = Receipt{MessageID: id, State: "written"}
        s.messages[id] = Message{ID: id, Text: "old"}
    }
    full := s.deliver(context.Background(), Message{ID: "new_event", Text: "New"}, true)
    if full.State != "rejected" || full.ReasonCode != "CLAUDE_CHANNEL_CAPACITY" {
        t.Fatal(full)
    }
    oldest := s.order[0]
    s.receipts[oldest] = Receipt{MessageID: oldest, State: "processed"}
    left, right := net.Pipe()
    defer left.Close()
    defer right.Close()
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    s.connection, _ = (&mcp.IOTransport{Reader: left, Writer: left}).Connect(ctx)
    consumed := make(chan struct{})
    go func() { var note any; _ = json.NewDecoder(right).Decode(&note); close(consumed) }()
    delivered := s.deliver(ctx, Message{ID: "new_event", Text: "New"}, true)
    if delivered.State != "written" || len(s.receipts) != 256 {
        t.Fatal(delivered, len(s.receipts))
    }
    if _, ok := s.receipts[oldest]; ok {
        t.Fatal("processed receipt was not evicted")
    }
    if s.receipts[s.order[0]].State != "written" {
        t.Fatal("unconfirmed evidence evicted")
    }
    <-consumed
}
