package agentdelivery

import (
    "context"
    "errors"
    "net/http"
    "net/http/httptest"
    "sync/atomic"
    "testing"
)

type failingWebhookTransport struct{ calls int }

func (t *failingWebhookTransport) RoundTrip(r *http.Request) (*http.Response, error) {
    t.calls++
    if r.GetBody != nil {
        return nil, errors.New("request body incorrectly replayable")
    }
    return nil, errors.New("lost reply after possible send")
}

func TestWebhookPossibleSendHasNoReplayableBody(t *testing.T) {
    transport := &failingWebhookTransport{}
    receipt, err := (Webhook{Transport: transport}).Deliver(context.Background(), Destination{Kind: "webhook", Endpoint: "https://example.invalid/event"}, Message{ID: "event_1", Text: "Finished"})
    if err != ErrUnconfirmed || receipt.State != "unconfirmed" || receipt.RetrySafe || transport.calls != 1 {
        t.Fatal(receipt, err, transport.calls)
    }
}

func TestWebhookAcceptanceAndRedirectBoundary(t *testing.T) {
    var forwarded atomic.Int32
    other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(204) }))
    defer other.Close()
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/redirect" {
            w.Header().Set("Location", other.URL)
            w.WriteHeader(307)
            return
        }
        if r.Header.Get("Idempotency-Key") != "event_1" {
            t.Error("missing message identity")
        }
        w.WriteHeader(202)
    }))
    defer server.Close()
    receipt, err := (Webhook{}).Deliver(context.Background(), Destination{Kind: "webhook", Endpoint: server.URL}, Message{ID: "event_1", Text: "Finished"})
    if err != nil || receipt.State != "accepted" || receipt.State == "processed" {
        t.Fatal(receipt, err)
    }
    receipt, err = (Webhook{}).Deliver(context.Background(), Destination{Kind: "webhook", Endpoint: server.URL + "/redirect"}, Message{ID: "event_1", Text: "Finished"})
    if err == nil || receipt.State != "rejected" || forwarded.Load() != 0 {
        t.Fatal(receipt, err, forwarded.Load())
    }
}
