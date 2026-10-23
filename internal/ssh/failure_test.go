package ssh

import (
    "github.com/elebirds/loaf/internal/fault"
    "testing"
)

func TestFailureKindsUseEvidenceWithoutAttributingNetworkOwnership(t *testing.T) {
    for _, tc := range []struct{ detail, kind string }{{"Permission denied (publickey)", "authentication"}, {"Host key verification failed", "host_trust"}, {"Could not resolve hostname private-host", "name_resolution"}, {"Connection refused", "connection_refused"}, {"Operation timed out", "connection_timeout"}, {"Connection reset by peer", "connection_reset"}, {"Connection closed by unknown port", "connection_closed"}, {"opaque failure", "ssh_transport"}} {
        err := fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": tc.detail})
        if got := FailureKind(err); got != tc.kind {
            t.Fatal(got, tc)
        }
    }
}
