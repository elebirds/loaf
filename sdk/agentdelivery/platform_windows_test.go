package agentdelivery

import (
    "context"
    "errors"
    "testing"
)

func TestWindowsUnixAdaptersAreExplicitlyUnsupported(t *testing.T) {
    client := Client{}
    for _, kind := range []string{"codex", "codex-desktop", "claude-channel", "claude-resume"} {
        destination := Destination{Kind: kind, Endpoint: "/private/endpoint", SessionID: "exact_session"}
        capability, err := client.Probe(context.Background(), destination)
        if !errors.Is(err, ErrUnsupported) || capability.Available || capability.ReasonCode != "DESTINATION_UNSUPPORTED" {
            t.Fatal(kind, capability, err)
        }
        receipt, err := client.Deliver(context.Background(), destination, Message{ID: "event_1", Text: "Done"})
        if !errors.Is(err, ErrUnsupported) || receipt.State != "rejected" || receipt.ReasonCode != "DESTINATION_UNSUPPORTED" {
            t.Fatal(kind, receipt, err)
        }
    }
}

func TestWindowsDirectAdapterMethodsAreUnsupported(t *testing.T) {
    for kind, sender := range map[string]Sender{"codex": Codex{}, "codex-desktop": CodexDesktop{}, "claude-channel": ClaudeChannel{}} {
        destination := Destination{Kind: kind, Endpoint: "/private/endpoint", SessionID: "exact_session"}
        capability, err := sender.Probe(context.Background(), destination)
        if !errors.Is(err, ErrUnsupported) || capability.ReasonCode != "DESTINATION_UNSUPPORTED" {
            t.Fatal(kind, capability, err)
        }
        receipt, err := sender.Deliver(context.Background(), destination, Message{ID: "event_1", Text: "Done"})
        if !errors.Is(err, ErrUnsupported) || receipt.State != "rejected" || receipt.RetrySafe || receipt.ReasonCode != "DESTINATION_UNSUPPORTED" {
            t.Fatal(kind, receipt, err)
        }
    }
}
