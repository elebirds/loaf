package integration

import (
    "context"
    "os"
    "os/exec"
    "testing"
    "time"

    execution "github.com/elebirds/loaf/internal/run"
)

func TestHostNativeScopedRecovery(t *testing.T) {
    binary := os.Getenv("LOAF_HOST_SSH_TEST")
    if os.Getenv("LOAF_INTEGRATION") != "1" || binary == "" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    arch, err := f.client.RunnerArch(ctx, f.target, true)
    if err != nil {
        t.Fatal(err)
    }
    runner, err := (execution.Bootstrap{Remote: f.client}).Verify(ctx, f.target, arch, true)
    if err != nil {
        t.Fatal(err)
    }
    cmd := exec.CommandContext(ctx, binary, "-test.run=^TestNative(PrivateConnectionWithRsync|ResumableLogConnection)$", "-test.v")
    cmd.Env = append(os.Environ(), "LOAF_NATIVE_SCOPE_RUNNER="+runner.Path, "LOAF_NATIVE_SCOPE_CONFIG="+f.config, "LOAF_NATIVE_SCOPE_DEST=researcher@fixture", "LOAF_NATIVE_SCOPE_ROOT=/home/researcher/native 'space' scope")
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatal(string(out), err)
    }
    t.Log(string(out))
}
