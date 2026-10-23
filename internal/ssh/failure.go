package ssh

import (
    "context"
    "errors"
    "strings"

    "github.com/elebirds/loaf/internal/fault"
)

// FailureKind classifies evidence present in an error. It never attributes an
// ambiguous transport failure to a gateway, ISP or local network.
func FailureKind(err error) string {
    if err == nil {
        return ""
    }
    if errors.Is(err, context.Canceled) {
        return "canceled"
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return "observation_timeout"
    }
    var f *fault.Error
    if errors.As(err, &f) {
        detail, _ := f.Params["Detail"].(string)
        detail = strings.ToLower(detail)
        tests := []struct {
            kind     string
            patterns []string
        }{
            {"host_trust", []string{"host key verification failed", "remote host identification has changed"}},
            {"authentication", []string{"permission denied", "authentication failed", "too many authentication failures"}},
            {"configuration", []string{"bad configuration", "bad owner or permissions", "bad configuration option"}},
            {"name_resolution", []string{"could not resolve hostname", "name or service not known", "nodename nor servname"}},
            {"connection_refused", []string{"connection refused"}},
            {"connection_timeout", []string{"connection timed out", "operation timed out"}},
            {"connection_reset", []string{"connection reset"}},
            {"connection_closed", []string{"connection closed", "broken pipe", "unexpected end of file"}},
        }
        for _, test := range tests {
            for _, pattern := range test.patterns {
                if strings.Contains(detail, pattern) {
                    return test.kind
                }
            }
        }
        var scoped *scopedTransportError
        if errors.As(err, &scoped) && scoped.disconnected() {
            return "private_master_exit"
        }
        switch f.Code {
        case "CLOUD_FAILED":
            if f.Params["ProviderCode"] == "TRANSPORT" || f.Params["HTTPStatus"] == 429 || f.Params["HTTPStatus"] == 502 || f.Params["HTTPStatus"] == 503 || f.Params["HTTPStatus"] == 504 {
                return "cloud_transport"
            }
            return "cloud_api"
        case "SSH_FAILED", "PULL_FAILED":
            return "ssh_transport"
        case "CHECK_FAILED":
            return "remote_prerequisite"
        case "BUILD_REQUIRED", "RUNNER_REQUIRED", "VERSION_MISMATCH":
            return "runner"
        case "BAD_CONFIG":
            return "configuration"
        }
    }
    return "operation_failed"
}
