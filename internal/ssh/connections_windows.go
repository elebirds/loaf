package ssh

import (
    "context"
    "sync/atomic"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// Connections tracks the command lifetime. Native Windows uses one independent
// connection per operation, including a fresh resolution of frozen cloud IDs.
// This is a fixed platform policy, never a fallback after a transport failure.
type Connections struct{ closed atomic.Bool }

func NewConnections() *Connections { return &Connections{} }
func (s *Connections) Close()      { s.closed.Store(true) }
func (s *Connections) prepare(ctx context.Context, c *Client, target config.Target, batch bool) (connectionSession, error) {
    if s.closed.Load() {
        return connectionSession{}, fault.At(fault.New("SSH_FAILED", "ssh_failed", nil), "ssh_connect")
    }
    return c.prepareDirect(ctx, target, batch)
}
