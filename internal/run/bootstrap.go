// Package run orchestrates local operations that require the remote runner.
package run

import (
    "context"

    "github.com/elebirds/loaf/internal/bundle"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// Deployer is the SSH side effect required by runner verification.
type Deployer interface {
    DeployRunner(context.Context, config.Target, string, []byte, bool) (string, error)
}

// Bootstrap composes packaged runner assets with the existing SSH transport.
type Bootstrap struct{ Remote Deployer }

// RunnerInfo identifies the verified binary without exposing a general remote API.
type RunnerInfo struct {
    Path    string `json:"path"`
    Version string `json:"version"`
    Bytes   int    `json:"bytes"`
}

// Verify deploys and checks the matching runner only when explicitly requested.
func (b Bootstrap) Verify(ctx context.Context, target config.Target, arch string, batch bool) (RunnerInfo, error) {
    goarch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[arch]
    binary, err := bundle.Runner(goarch)
    if err != nil {
        return RunnerInfo{}, fault.Wrap("BUILD_REQUIRED", "runner_missing", err)
    }
    path, err := b.Remote.DeployRunner(ctx, target, wire.Version, binary, batch)
    if err != nil {
        return RunnerInfo{}, fault.Wrap("SSH_FAILED", "runner_failed", err)
    }
    return RunnerInfo{path, wire.Version, len(binary)}, nil
}
