package ssh

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
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
    contents, err := os.ReadFile(file)
    if err != nil {
        t.Fatal(err)
    }
    // Exercise ordinary foreground Exec with an explicit native trust policy.
    // The fixture alone disables authentication so acceptance is not readiness.
    native := filepath.Join(t.TempDir(), "config")
    if err := os.WriteFile(native, append([]byte("Host *\n    PreferredAuthentications none\n"), contents...), 0600); err != nil {
        t.Fatal(err)
    }
    t.Setenv("SSH_ASKPASS_REQUIRE", "never")
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    _, err = (&Client{Binary: "ssh"}).Exec(ctx, config.Target{Destination: "fixture", Args: []string{"-F", native, "-o", "StrictHostKeyChecking=ask"}}, ExecInput{Args: []string{"/bin/true"}}, os.Stdin, os.Stdout, os.Stderr)
    if FailureKind(err) != "authentication" {
        t.Fatal(FailureKind(err), err)
    }
    fmt.Println("LOAF_NATIVE_TRUST_COMPLETED")
}
