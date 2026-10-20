package integration

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    execution "github.com/elebirds/loaf/internal/run"
)

func TestPackagedLinuxClientWithNativeGNUrsync(t *testing.T) {
    archive := os.Getenv("LOAF_LINUX_ARCHIVE")
    if os.Getenv("LOAF_INTEGRATION") != "1" || archive == "" {
        t.Skip("run bash scripts/integration.sh for the packaged Linux client")
    }
    f := newFixture(t)
    f.shell(`mkdir -p /tmp/linux-client /tmp/linux-project`)
    command(t, "docker", "cp", archive, f.id+":/tmp/linux-client/package.tar.gz")
    command(t, "docker", "cp", f.key, f.id+":/tmp/linux-client/identity")
    command(t, "docker", "cp", f.known, f.id+":/tmp/linux-client/known_hosts")
    config := filepath.Join(t.TempDir(), "config")
    text := "Host self\n    HostName 127.0.0.1\n    User researcher\n    IdentityFile /tmp/linux-client/identity\n    IdentityAgent none\n    IdentitiesOnly yes\n    UserKnownHostsFile /tmp/linux-client/known_hosts\n    GlobalKnownHostsFile /dev/null\n    StrictHostKeyChecking yes\n"
    if err := os.WriteFile(config, []byte(text), 0600); err != nil {
        t.Fatal(err)
    }
    command(t, "docker", "cp", config, f.id+":/tmp/linux-client/config")
    command(t, "docker", "exec", f.id, "chown", "-R", "researcher:researcher", "/tmp/linux-client")
    root := "/home/researcher/linux 'quoted' mirror space"
    f.shell(`tar -xzf /tmp/linux-client/package.tar.gz -C /tmp/linux-client; cd /tmp/linux-project; printf code >'中文 space.py'; printf literal >'back\slash[1]*?.py'; printf secret >.env; ln -s '中文 space.py' linked.py; printf 'remote_dir = "%s"\n' "$1" >loaf.toml`, root)
    invoke := func(args ...string) []byte {
        t.Helper()
        // All client configuration, credentials, and project data are fixture-owned.
        argv := []string{"exec", "--user", "researcher", "-w", "/tmp/linux-project", "-e", "XDG_CONFIG_HOME=/tmp/linux-config", "-e", "XDG_STATE_HOME=/tmp/linux-state", f.id, "/tmp/linux-client/loaf"}
        out := command(t, "docker", append(argv, args...)...)
        if !json.Valid([]byte(out)) {
            t.Fatal(out)
        }
        return []byte(out)
    }
    invoke("add", "self", "--ssh", "ssh -F /tmp/linux-client/config self", "--json")
    invoke("use", "self", "--json")
    invoke("sync", "--json")
    out := invoke("sync", "--json")
    var result struct {
        Data struct {
            ChangedFiles int `json:"changed_files"`
            Policy       struct{ Mode string }
        }
    }
    if json.Unmarshal(out, &result) != nil || result.Data.ChangedFiles != 0 || result.Data.Policy.Mode != "mirror" {
        t.Fatal(string(out))
    }
    f.shell(`test -f "$1/cache/中文 space.py"; test -f "$1/cache/back\slash[1]*?.py"; test ! -e "$1/cache/.env"; test "$(readlink "$1/cache/linked.py")" = '中文 space.py'`, root)
    out = invoke("run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", "mkdir outputs; printf result >outputs/result.txt; printf 'linux 中文\\n'; exit 7")
    var ran struct {
        Data execution.View `json:"data"`
    }
    if json.Unmarshal(out, &ran) != nil || ran.Data.Status != "failed" || ran.Data.ExitCode == nil || *ran.Data.ExitCode != 7 || ran.Data.Log == nil || !strings.Contains(ran.Data.Log.Text, "linux 中文") {
        t.Fatal(string(out))
    }
    invoke("ps", ran.Data.ID, "--json")
    invoke("logs", ran.Data.ID, "--json")
    for _, want := range []int{1, 0} {
        out = invoke("pull", ran.Data.ID, "--json")
        var downloaded struct {
            Data struct {
                Destination  string `json:"destination"`
                ChangedFiles *int   `json:"changed_files"`
            } `json:"data"`
        }
        if json.Unmarshal(out, &downloaded) != nil || downloaded.Data.ChangedFiles == nil || *downloaded.Data.ChangedFiles != want {
            t.Fatal(string(out))
        }
        f.shell(`test "$(cat "$1/outputs/result.txt")" = result`, downloaded.Data.Destination)
    }
    // The fixture's separate 1 MiB tmpfs creates real ENOSPC without filling a
    // host disk. The previous completed file must survive the failed update.
    f.shell(`dd if=/dev/urandom of="$1/runs/$2/work/outputs/result.txt" bs=1048576 count=2 2>/dev/null; mkdir -p /tmp/loaf-full/result/outputs; printf previous >/tmp/loaf-full/result/outputs/result.txt`, ran.Data.RemoteDir, ran.Data.ID)
    failure := f.shell(`set -eu; cd /tmp/linux-project; if XDG_CONFIG_HOME=/tmp/linux-config XDG_STATE_HOME=/tmp/linux-state /tmp/linux-client/loaf pull "$1" --to /tmp/loaf-full/result --json >/tmp/disk-full.json; then exit 1; else test "$?" -eq 1; fi; test "$(cat /tmp/loaf-full/result/outputs/result.txt)" = previous; cat /tmp/disk-full.json`, ran.Data.ID)
    if !strings.Contains(failure, `"code":"PULL_FAILED"`) || !strings.Contains(failure, "No space left on device") {
        t.Fatal("disk-full error not retained", failure)
    }
    // Retry to another destination without restarting the failed experiment.
    out = invoke("pull", ran.Data.ID, "--to", "/tmp/linux-retry", "--json")
    if !json.Valid(out) {
        t.Fatal(string(out))
    }
    f.shell(`test "$(wc -c </tmp/linux-retry/outputs/result.txt)" -eq 2097152`)
    out = invoke("run", "--json", "--", "/bin/sh", "-c", `trap '' TERM; echo ready >ready; while :; do sleep .05; done`)
    if err := json.Unmarshal(out, &ran); err != nil {
        t.Fatal(err)
    }
    waitRunFile(t, f, ran.Data, "work/ready")
    out = invoke("stop", ran.Data.ID, "--grace", "0s", "--json")
    if json.Unmarshal(out, &ran) != nil || ran.Data.Status != "stopped" || ran.Data.Signal == "" || ran.Data.StopGraceMillis == nil || *ran.Data.StopGraceMillis != 0 {
        t.Fatal(string(out))
    }
}
