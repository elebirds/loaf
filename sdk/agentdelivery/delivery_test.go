package agentdelivery

import "testing"

func TestExplicitDestinationAndBoundedMessage(t *testing.T) {
    for _, d := range []Destination{{Kind: "codex"}, {Kind: "claude-channel", SessionID: "../other"}, {Kind: "unknown", SessionID: "session"}} {
        if d.Validate() == nil {
            t.Fatalf("accepted ambiguous destination: %#v", d)
        }
    }
    if err := (Destination{Kind: "codex", SessionID: "thread_1"}).Validate(); err != nil {
        t.Fatal(err)
    }
    if err := (Message{ID: "evt_1", Text: "Finished"}).Validate(); err != nil {
        t.Fatal(err)
    }
    if (Message{Text: "Finished"}).Validate() == nil {
        t.Fatal("missing stable message identity")
    }
}

func TestTransportWriteIsNotAcceptance(t *testing.T) {
    for _, state := range []string{"written", "unconfirmed", "rejected", "pending"} {
        if (Receipt{State: state}).Accepted() {
            t.Fatalf("%s is not acceptance evidence", state)
        }
    }
    if !(Receipt{State: "accepted"}).Accepted() || !(Receipt{State: "processed"}).Accepted() {
        t.Fatal("explicit evidence lost")
    }
}
