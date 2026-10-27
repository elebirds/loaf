package integration

import (
    "bytes"
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"

    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
)

func TestLostSubmissionConfirmationUsesTheOriginalID(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := t.TempDir()
    for _, args := range [][]string{{"machine", "add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"machine", "use", "lab", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    realSSH, err := exec.LookPath("ssh")
    if err != nil {
        t.Fatal(err)
    }
    bin := filepath.Join(f.dir, "fault-bin")
    if err := os.Mkdir(bin, 0700); err != nil {
        t.Fatal(err)
    }
    marker := filepath.Join(bin, "drop-status")
    counter := filepath.Join(bin, "launch-count")
    // Test-only failure injection leaves the native rsync stream untouched. A
    // start is executed normally, but its confirmation is deliberately dropped.
    script := `#!/bin/sh
for argument do
    case "$argument" in *'__receive'*|*'/bin/sh -s --'*|*'umask 077'*|-V) exec ` + ssh.Quote(realSSH) + ` "$@" ;; esac
done
payload=$(mktemp)
trap 'rm -f "$payload"' EXIT HUP INT TERM
cat >"$payload"
if grep -q '"action":"launch"' "$payload"; then
    printf 'launch\n' >>` + ssh.Quote(counter) + `
    ` + ssh.Quote(realSSH) + ` "$@" <"$payload" >/dev/null
    exit 255
fi
if [ -e ` + ssh.Quote(marker) + ` ] && grep -q '"action":"status"' "$payload"; then
    ` + ssh.Quote(realSSH) + ` "$@" <"$payload" >/dev/null
    exit 255
fi
` + ssh.Quote(realSSH) + ` "$@" <"$payload"
exit $?
`
    if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0700); err != nil {
        t.Fatal(err)
    }
    t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
    recovered := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "echo once >> launch-count; echo recovered")
    f.shell(`test "$(wc -l <"$1/runs/$2/work/launch-count")" -eq 1`, recovered.RemoteDir, recovered.ID)
    calls, err := os.ReadFile(counter)
    if err != nil || strings.Count(string(calls), "launch") != 1 {
        t.Fatal(string(calls), err)
    }
    if err := os.WriteFile(marker, nil, 0600); err != nil {
        t.Fatal(err)
    }
    code, out := cli(t, project, "run", "--json", "--", "/bin/sh", "-c", "echo once >> launch-count; echo uncertain")
    if code != 3 || !bytes.Contains(out, []byte(`"code":"SUBMISSION_UNKNOWN"`)) {
        t.Fatal(code, string(out))
    }
    var response struct {
        Data execution.View `json:"data"`
    }
    if err := json.Unmarshal(out, &response); err != nil || response.Data.ID == "" {
        t.Fatal(string(out), err)
    }
    uncertain := response.Data
    calls, _ = os.ReadFile(counter)
    if strings.Count(string(calls), "launch") != 2 {
        t.Fatal("automatically resubmitted", string(calls))
    }
    os.Remove(marker)
    latest := runCLI(t, project, "logs", "--json")
    if latest.ID != uncertain.ID || latest.ID == recovered.ID || latest.Log == nil || !strings.Contains(latest.Log.Text, "uncertain") {
        t.Fatalf("uncertain latest replaced: %+v", latest)
    }
    f.shell(`test "$(wc -l <"$1/runs/$2/work/launch-count")" -eq 1`, uncertain.RemoteDir, uncertain.ID)
}
