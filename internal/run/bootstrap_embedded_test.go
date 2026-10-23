//go:build loaf_embed

package run

import (
    "context"
    "errors"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

type failingDeployment struct{ err error }

func (d failingDeployment) DeployRunner(context.Context, config.Target, string, []byte, bool) (string, error) {
    return "", d.err
}

func TestBootstrapRetainsOriginalCodeHintAndDiagnostic(t *testing.T) {
    for _, code := range []string{"SSH_FAILED", "CLOUD_FAILED", "CLOUD_AUTH_REQUIRED"} {
        t.Run(code, func(t *testing.T) {
            original := fault.New(code, "ssh_failed", map[string]any{"Detail": "Connection closed by fixture", "ProviderCode": "TRANSPORT"})
            original.Hint = "cloud_ssh_hint"
            _, err := (Bootstrap{Remote: failingDeployment{fault.At(original, "ssh_connect")}}).Verify(context.Background(), config.Target{}, "aarch64", true)
            var actual *fault.Error
            if !errors.As(err, &actual) || actual != original || fault.Stage(err) != "ssh_connect" || actual.Hint != original.Hint || actual.Params["Detail"] != original.Params["Detail"] {
                t.Fatal(err, actual)
            }
        })
    }
}

func TestBootstrapRetainsStructuredRunnerFailureCodes(t *testing.T) {
    for _, code := range []string{"VERSION_MISMATCH", "BAD_FORMAT", "BAD_RESPONSE"} {
        original := &ssh.RemoteError{Code: code}
        _, err := (Bootstrap{Remote: failingDeployment{original}}).Verify(context.Background(), config.Target{}, "aarch64", true)
        var actual *fault.Error
        if !errors.As(err, &actual) || actual.Code != code || !errors.Is(err, original) || fault.Stage(err) != "runner_verify" {
            t.Fatal(code, err, actual)
        }
    }
}
