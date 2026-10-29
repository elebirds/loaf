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
        binary := filepath.Join(dir, "ssh")
        count := filepath.Join(dir, "calls")
        script := "#!/bin/sh\necho x >> '" + count + "'\nprintf '%s' '" + tc.message + " private-endpoint secret-marker' >&2\nexit 255\n"
        if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
            t.Fatal(err)
        }
        result, err := (&Client{Binary: binary}).Exec(context.Background(), config.Target{Destination: "fixture"}, ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
        wrapped := fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", err)
        if result.Status != "unconfirmed" || FailureKind(wrapped) != tc.kind || FailureReason(wrapped) != tc.reason || RetryableConnection(wrapped) != tc.retry {
            t.Fatal(result, wrapped, FailureKind(wrapped))
        }
        if strings.Contains(err.Error(), "secret-marker") {
            t.Fatal("diagnostic escaped", err)
        }
        calls, _ := os.ReadFile(count)
        if string(calls) != "x\n" {
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
    file := filepath.Join(t.TempDir(), "ssh")
    output := filepath.Join(t.TempDir(), "args")
    if err := os.WriteFile(file, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+output+"'\nexit 0\n"), 0700); err != nil {
        t.Fatal(err)
    }
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
