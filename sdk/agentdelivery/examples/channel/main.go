// Command channel is the optional stdio MCP entry for an explicitly enabled session.
package main

import (
    "context"
    "flag"
    "os"
    "os/signal"
    "syscall"

    "github.com/elebirds/loaf/sdk/agentdelivery"
)

func main() {
    socket := flag.String("socket", "", "short absolute Unix socket path in a private directory")
    session := flag.String("session", "", "explicit unique session binding label")
    flag.Parse()
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()
    if err := agentdelivery.RunClaudeChannel(ctx, *socket, *session, os.Stdin, os.Stdout); err != nil {
        os.Exit(1)
    }
}
