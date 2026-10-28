package agentdelivery

import (
    "bytes"
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/url"
    "time"
)

// Webhook delivers JSON to one explicitly configured HTTP endpoint.
// HTTP acceptance is receiver evidence, never proof of Agent processing.
type Webhook struct{ Transport http.RoundTripper }

func webhookURL(d Destination) bool {
    if d.Kind != "webhook" || d.Validate() != nil {
        return false
    }
    u, err := url.Parse(d.Endpoint)
    return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Fragment == ""
}

// Probe validates configuration without claiming remote endpoint health.
func (w Webhook) Probe(_ context.Context, d Destination) (Capabilities, error) {
    if !webhookURL(d) {
        return Capabilities{ReasonCode: "DESTINATION_INVALID"}, ErrDestination
    }
    return Capabilities{Available: true}, nil
}

// Deliver never follows redirects or replays a possible send.
func (w Webhook) Deliver(ctx context.Context, d Destination, m Message) (Receipt, error) {
    receipt := Receipt{MessageID: m.ID, State: "rejected"}
    if !webhookURL(d) {
        return receipt, ErrDestination
    }
    if m.Validate() != nil {
        return receipt, ErrMessage
    }
    if err := ctx.Err(); err != nil {
        receipt.RetrySafe = true
        return receipt, err
    }
    body, _ := json.Marshal(m)
    request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, bytes.NewReader(body))
    if err != nil {
        return receipt, ErrDestination
    }
    request.Header.Set("Content-Type", "application/json")
    request.Header.Set("Idempotency-Key", m.ID)
    client := &http.Client{Transport: w.Transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
    response, err := client.Do(request)
    if err != nil {
        receipt.State = "unconfirmed"
        receipt.ReasonCode = "WEBHOOK_SEND_UNCONFIRMED"
        return receipt, ErrUnconfirmed
    }
    defer response.Body.Close()
    _, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
    if response.StatusCode >= 200 && response.StatusCode < 300 {
        receipt.State = "accepted"
        return receipt, nil
    }
    receipt.ReasonCode = "WEBHOOK_REJECTED"
    receipt.RetrySafe = response.StatusCode == http.StatusTooManyRequests
    if response.StatusCode >= 500 {
        receipt.State = "unconfirmed"
        receipt.RetrySafe = false
        return receipt, ErrUnconfirmed
    }
    return receipt, ErrUnavailable
}
