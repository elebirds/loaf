package client

import (
    "context"
    "github.com/elebirds/loaf/internal/state/notifications"
    "github.com/elebirds/loaf/sdk/agentdelivery"
    "regexp"
)

// All conversions between Loaf's durable protocol and the standalone SDK live here.
func savedAddress(d agentdelivery.Destination) notifications.Address {
    return notifications.Address{Kind: d.Kind, Endpoint: d.Endpoint, SessionID: d.SessionID, CWD: d.CWD, BearerEnv: d.BearerRef, AllowInsecureHTTP: d.AllowInsecureHTTP}
}
func transportAddress(d notifications.Address) agentdelivery.Destination {
    return agentdelivery.Destination{Kind: d.Kind, Endpoint: d.Endpoint, SessionID: d.SessionID, CWD: d.CWD, BearerRef: d.BearerEnv, AllowInsecureHTTP: d.AllowInsecureHTTP}
}
func savedEvidence(r agentdelivery.Receipt) notifications.Evidence {
    return notifications.Evidence{MessageID: r.MessageID, State: r.State, OperationID: r.OperationID, ReasonCode: r.ReasonCode, RetrySafe: r.RetrySafe}
}
func transportEvidence(r notifications.Evidence) agentdelivery.Receipt {
    return agentdelivery.Receipt{MessageID: r.MessageID, State: r.State, OperationID: r.OperationID, ReasonCode: r.ReasonCode, RetrySafe: r.RetrySafe}
}
func transportMessage(m notifications.Message) agentdelivery.Message {
    return agentdelivery.Message{ID: m.ID, Subject: m.Subject, Text: m.Text}
}

var bearerEnvironment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func defaultNotificationSender(lookup func(string) (string, bool)) agentdelivery.Sender {
    return agentdelivery.Client{Webhook: agentdelivery.Webhook{ResolveBearer: func(ctx context.Context, ref string) (string, error) {
        if err := ctx.Err(); err != nil {
            return "", err
        }
        if !bearerEnvironment.MatchString(ref) {
            return "", agentdelivery.ErrDestination
        }
        if lookup == nil {
            return "", agentdelivery.ErrUnavailable
        }
        token, ok := lookup(ref)
        if !ok || token == "" {
            return "", agentdelivery.ErrUnavailable
        }
        return token, nil
    }}}
}
