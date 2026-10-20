package integration

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"
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
}
