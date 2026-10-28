// Command send demonstrates using the delivery SDK without Loaf application code.
package main

import (
    "context"
    "encoding/json"
    "flag"
    "os"
    "time"

    "github.com/elebirds/loaf/sdk/agentdelivery"
)

func main() {
    kind := flag.String("kind", "codex", "codex, claude-channel or webhook")
    endpoint := flag.String("endpoint", "", "explicit private endpoint")
    session := flag.String("session", "", "explicit session identity")
    id := flag.String("id", "", "stable caller message identity")
    text := flag.String("text", "", "authorized fact to deliver")
    flag.Parse()
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    receipt, err := (agentdelivery.Client{}).Deliver(ctx, agentdelivery.Destination{Kind: *kind, Endpoint: *endpoint, SessionID: *session}, agentdelivery.Message{ID: *id, Subject: "background operation", Text: *text})
    _ = json.NewEncoder(os.Stdout).Encode(receipt)
    if err != nil {
        os.Exit(1)
    }
}
