package ssh

import (
    "errors"
    "io"
    "strings"
)

// diagnosticWriter forwards the native stream unchanged, retaining only a
// bounded private prefix for classification. Its contents never enter errors.
type diagnosticWriter struct {
    out  io.Writer
    data []byte
}

func (w *diagnosticWriter) Write(p []byte) (int, error) {
    if left := 8192 - len(w.data); left > 0 {
        w.data = append(w.data, p[:min(left, len(p))]...)
    }
    if w.out == nil {
        return len(p), nil
    }
    return w.out.Write(p)
}

type classifiedConnectionFailure struct {
    cause        error
    kind, reason string
}

func (e *classifiedConnectionFailure) Error() string { return e.cause.Error() }
func (e *classifiedConnectionFailure) Unwrap() error { return e.cause }

func classifyConnection(err error, diagnostic []byte) error {
    if err == nil {
        return nil
    }
    kind, reason := diagnosticKind(strings.ToLower(string(diagnostic)))
    return &classifiedConnectionFailure{cause: err, kind: kind, reason: reason}
}

func diagnosticKind(detail string) (string, string) {
    tests := []struct {
        kind, reason string
        patterns     []string
    }{
        {"host_trust", "host_key_changed", []string{"remote host identification has changed", "offending ", "host key has changed"}},
        {"host_trust", "host_key_unknown", []string{"host key verification failed", "no host key is known", "authenticity of host"}},
        {"authentication", "authentication", []string{"permission denied", "authentication failed", "too many authentication failures"}},
        {"configuration", "configuration", []string{"bad configuration", "bad owner or permissions", "bad configuration option"}},
        {"name_resolution", "name_resolution", []string{"could not resolve hostname", "name or service not known", "nodename nor servname"}},
        {"connection_refused", "connection_refused", []string{"connection refused"}},
        {"connection_timeout", "connection_timeout", []string{"connection timed out", "operation timed out"}},
        {"connection_reset", "connection_reset", []string{"connection reset"}},
        {"connection_closed", "connection_closed", []string{"connection closed", "broken pipe", "unexpected end of file"}},
    }
    for _, test := range tests {
        for _, pattern := range test.patterns {
            if strings.Contains(detail, pattern) {
                return test.kind, test.reason
            }
        }
    }
    return "ssh_transport", "unknown_transport"
}

// FailureReason refines safe connection classifications without exposing SSH text.
func FailureReason(err error) string {
    var failure *classifiedConnectionFailure
    if errors.As(err, &failure) {
        return failure.reason
    }
    return ""
}

// RetryableConnection permits re-observation by a fixed health probe, never
// automatic replay of a foreground command or cloud mutation.
func RetryableConnection(err error) bool {
    switch FailureKind(err) {
    case "connection_refused", "connection_timeout", "connection_reset", "connection_closed", "cloud_transport":
        return true
    }
    return false
}
