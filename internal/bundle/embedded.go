//go:build loaf_embed

package bundle

import (
    "embed"
    "fmt"
)

//go:embed assets/loaf-runner-linux-*
var runners embed.FS

// Runner returns the Linux runner for a supported Go architecture.
func Runner(arch string) ([]byte, error) {
    if arch != "amd64" && arch != "arm64" {
        return nil, fmt.Errorf("UNSUPPORTED_ARCH: %s", arch)
    }
    return runners.ReadFile("assets/loaf-runner-linux-" + arch)
}
