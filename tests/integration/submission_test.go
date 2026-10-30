package integration

import (
    "bytes"
    "encoding/json"
    "os"
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
    bin := t.TempDir()
    marker := filepath.Join(bin, "drop-status")
    counter := filepath.Join(bin, "launch-count")
    useTransportProxy(t, transportFault{DropAction: "launch", DropRead: "status", Marker: marker, Counter: counter})
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
