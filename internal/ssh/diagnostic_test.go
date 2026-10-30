package ssh

import (
    "context"
    "errors"
    "io"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func TestExecConnectionClassificationIsBoundedPrivateAndNeverReplayed(t *testing.T) {
    for _, tc := range []struct {
        message, kind, reason string
        retry                 bool
    }{
        {"Host key verification failed", "host_trust", "host_key_unknown", false},
        {"REMOTE HOST IDENTIFICATION HAS CHANGED", "host_trust", "host_key_changed", false},
        {"Permission denied (publickey,password)", "authentication", "authentication", false},
        {"Connection refused", "connection_refused", "connection_refused", true},
        {"Operation timed out", "connection_timeout", "connection_timeout", true},
    } {
        dir := t.TempDir()
        count := filepath.Join(dir, "calls")
        binary := fixtureProcess(t, processFixture{Count: count, Stderr: tc.message + " private-endpoint secret-marker", Exit: 255})
        result, err := (&Client{Binary: binary}).Exec(context.Background(), config.Target{Destination: "fixture"}, ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
        wrapped := fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", err)
        if result.Status != "unconfirmed" || FailureKind(wrapped) != tc.kind || FailureReason(wrapped) != tc.reason || RetryableConnection(wrapped) != tc.retry {
            t.Fatal(result, wrapped, FailureKind(wrapped))
        }
        if strings.Contains(err.Error(), "secret-marker") {
            t.Fatal("diagnostic escaped", err)
        }
        calls, _ := os.ReadFile(count)
        if string(calls) != "call\n" {
            t.Fatal("foreground command replayed", string(calls))
        }
    }
    w := &diagnosticWriter{}
    _, _ = w.Write([]byte(strings.Repeat("x", 32000)))
    if len(w.data) != 8192 {
        t.Fatal(len(w.data))
    }
}

func TestNativeHostTrustDisablesAuthenticationAndRejectsOtherCommands(t *testing.T) {
    output := filepath.Join(t.TempDir(), "args")
    file := fixtureProcess(t, processFixture{Arguments: output})
    client := &Client{Binary: file}
    _, err := client.Exec(context.Background(), config.Target{Destination: "fixture"}, ExecInput{Args: []string{"/bin/true"}, TrustOnly: true}, nil, io.Discard, io.Discard)
    args, _ := os.ReadFile(output)
    for _, required := range []string{"StrictHostKeyChecking=ask", "PreferredAuthentications=none", "PasswordAuthentication=no", "PubkeyAuthentication=no"} {
        if err != nil || !strings.Contains(string(args), required) {
            t.Fatal(err, string(args))
        }
    }
    _, err = client.Exec(context.Background(), config.Target{Destination: "fixture"}, ExecInput{Args: []string{"rm", "file"}, TrustOnly: true}, nil, io.Discard, io.Discard)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
        t.Fatal(err)
    }
}
