//go:build !loaf_embed

package run

import (
    "context"
    "errors"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

type unexpectedDeploy struct{ t *testing.T }

func (f unexpectedDeploy) DeployRunner(context.Context, config.Target, string, []byte, bool) (string, error) {
    f.t.Fatal("deployed a development stub")
    return "", nil
}

func TestMissingBundleIsActionableAndDoesNotConnect(t *testing.T) {
    b := Bootstrap{Remote: unexpectedDeploy{t}}
    _, err := b.Verify(context.Background(), config.Target{}, "aarch64", true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "BUILD_REQUIRED" || f.Message != "runner_missing" {
        t.Fatal(err)
    }
}
