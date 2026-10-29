package ssh

import (
    "context"
    "fmt"
    "os"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
)

// Invoked in a controlling PTY by the isolated Docker fixture; never uses the
// developer's SSH files or any externally provisioned resource.
func TestNativeCloudTrustTerminalChild(t *testing.T) {
    file := os.Getenv("LOAF_NATIVE_TRUST_CONFIG")
    if file == "" {
        t.Skip("isolated native trust child")
    }
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    _, err := (&Client{Binary: "ssh"}).Exec(ctx, config.Target{Destination: "fixture", Args: []string{"-F", file}}, ExecInput{Args: []string{"/bin/true"}, TrustOnly: true}, os.Stdin, os.Stdout, os.Stderr)
    if FailureKind(err) != "authentication" {
        t.Fatal(FailureKind(err), err)
    }
    fmt.Println("LOAF_NATIVE_TRUST_COMPLETED")
}
