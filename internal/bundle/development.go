//go:build !loaf_embed

// Package bundle supplies matching Linux runner binaries in complete builds.
package bundle

import "errors"

// Runner reports missing generated assets in ordinary development builds.
func Runner(arch string) ([]byte, error) {
    return nil, errors.New("RUNNER_NOT_EMBEDDED: run bash scripts/build.sh")
}
