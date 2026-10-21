package integration

import (
    "os"
    "path/filepath"
    "testing"
)

// A private container session bus exercises the native Linux vault without
// mounting a user's keyring, credentials or session bus. HTTP remains simulated.
func TestLinuxCloudCredentialStores(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    staging := t.TempDir()
    if err := os.Chmod(staging, 0755); err != nil {
        t.Fatal(err)
    }
    for name, env := range map[string]string{"credentials.test": "LOAF_CREDENTIAL_TEST", "cli.test": "LOAF_CLI_TEST"} {
        data, err := os.ReadFile(os.Getenv(env))
        if err != nil {
            t.Fatal("missing Linux credential test binary", env, err)
        }
        if err := os.WriteFile(filepath.Join(staging, name), data, 0755); err != nil {
            t.Fatal(err)
        }
    }
    command(t, "tar", "-xzf", os.Getenv("LOAF_LINUX_ARCHIVE"), "-C", staging)
    script := `set -eu
printf '%s' fixture-vault-password | gnome-keyring-daemon --unlock --components=secrets
LOAF_TEST_KEYRING=1 /work/credentials.test -test.run '^TestNativeKeyringRoundTrip$' -test.v -test.timeout=30s
LOAF_TEST_NATIVE_VAULT=1 /work/cli.test -test.run '^TestCloudCredentialLifecycleAcrossProcesses$' -test.v -test.timeout=60s
/work/cli.test -test.run '^TestCloud(AcquisitionRecovery|PowerRecovery)AcrossProcesses$' -test.v -test.timeout=60s
DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/no-loaf-session-bus LOAF_TEST_KEYRING_UNAVAILABLE=1 /work/credentials.test -test.run '^TestNativeKeyringWithoutSessionBus$' -test.v -test.timeout=15s
/work/loaf --lang zh-Hans cloud login --help >/tmp/login-help
grep -q -- --credential-store /tmp/login-help
/work/loaf version --json
`
    start := `mkdir -p "$HOME" "$XDG_RUNTIME_DIR"
chmod 700 "$HOME" "$XDG_RUNTIME_DIR"
exec dbus-run-session -- sh -eu -c "$1"`
    output := command(t, "docker", "run", "--rm", "--network", "none", "--mount", "type=bind,source="+staging+",target=/work,readonly", "-e", "HOME=/tmp/loaf-home", "-e", "XDG_RUNTIME_DIR=/tmp/loaf-runtime", "loaf-credentials-test:local", "sh", "-eu", "-c", start, "loaf-vault-session", script)
    t.Log(output)
}
