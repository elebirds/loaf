package ssh

import (
    "bytes"
    "context"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func TestUXBCheckTrustReasonUsesExistingClassifier(t *testing.T) {
    for _, tc := range []struct{ diagnostic, reason string }{
        {"Host key verification failed", "host_key_unknown"},
        {"REMOTE HOST IDENTIFICATION HAS CHANGED", "host_key_changed"},
    } {
        original := fault.At(fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": tc.diagnostic + " private-endpoint"}), "preflight")
        if FailureKind(original) != "host_trust" || FailureReason(original) != tc.reason || fault.Stage(original) != "preflight" {
            t.Errorf("kind=%s reason=%s, want %s", FailureKind(original), FailureReason(original), tc.reason)
        }
    }
}

func TestUXBNativeConnectionClassificationPreservesStreamsAndSingleCall(t *testing.T) {
    for _, operation := range []string{"ssh", "forward"} {
        for _, tc := range []struct{ diagnostic, kind, reason string }{
            {"Host key verification failed", "host_trust", "host_key_unknown"},
            {"REMOTE HOST IDENTIFICATION HAS CHANGED", "host_trust", "host_key_changed"},
            {"Permission denied", "authentication", "authentication"},
            {"Bad configuration option", "configuration", "configuration"},
            {"Connection refused", "connection_refused", "connection_refused"},
            {"Operation timed out", "connection_timeout", "connection_timeout"},
            {"Connection closed", "connection_closed", "connection_closed"},
        } {
            t.Run(operation+"/"+tc.reason, func(t *testing.T) {
                dir := t.TempDir()
                count := filepath.Join(dir, "count")
                message := tc.diagnostic + " private-endpoint secret-marker\n"
                binary := fixtureProcess(t, processFixture{Stderr: message, Exit: 255, Count: count})
                client := &Client{Binary: binary}
                var native bytes.Buffer
                var err error
                if operation == "ssh" {
                    err = client.Interactive(context.Background(), config.Target{Destination: "fixture"}, nil, nil, &native)
                } else {
                    err = client.Forward(context.Background(), config.Target{Destination: "fixture"}, 16006, 6006, true, &native)
                }
                var problem *fault.Error
                if !errors.As(err, &problem) || problem.Code != "SSH_FAILED" || FailureKind(err) != tc.kind || FailureReason(err) != tc.reason {
                    t.Errorf("%v: kind=%s reason=%s", err, FailureKind(err), FailureReason(err))
                }
                if native.String() != message || strings.Contains(err.Error(), "secret-marker") {
                    t.Fatal("changed native stream or leaked captured diagnostics", native.String(), err)
                }
                calls, readErr := os.ReadFile(count)
                if readErr != nil || string(calls) != "call\n" {
                    t.Fatal("native operation replayed", string(calls), readErr)
                }
            })
        }
    }
}
