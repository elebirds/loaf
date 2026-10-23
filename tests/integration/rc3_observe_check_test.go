package integration

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    execution "github.com/elebirds/loaf/internal/run"
)

func TestPublicOfflinePreviewWaitAndMultiCheck(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    first := runCLI(t, project, "run", "--tag", "pair", "--json", "--", "/bin/sh", "-c", "printf first; sleep .3")
    second := runCLI(t, project, "run", "--tag", "pair", "--json", "--", "/bin/sh", "-c", "printf second; sleep .3")
    code, out := cli(t, project, "wait", "--tag", "pair", "--timeout", "10s", "--json")
    var waited struct {
        Data execution.WaitResult `json:"data"`
    }
    if code != 0 || json.Unmarshal(out, &waited) != nil || waited.Data.Completed != 2 || waited.Data.Selected != 2 || waited.Data.Examined != 2 {
        t.Fatal(code, string(out))
    }
    for _, id := range []string{first.ID, second.ID} {
        code, out = cli(t, project, "inspect", id, "--json")
        if code != 0 || !strings.Contains(string(out), `"last_observation"`) || !strings.Contains(string(out), `"status":"unknown"`) {
            t.Fatal(code, string(out))
        }
    }
    code, out = cli(t, project, "run", "--dry-run", "--json", "--", "echo", "not-submitted")
    if code != 0 || !strings.Contains(string(out), `"dry_run":true`) || !strings.Contains(string(out), `"command":["flag"]`) {
        t.Fatal(code, string(out))
    }
    code, out = cli(t, project, "add", "bad", "--ssh", "ssh -p 9 nobody@127.0.0.1", "--json")
    if code != 0 {
        t.Fatal(code, string(out))
    }
    report := filepath.Join(t.TempDir(), "diagnostic.json")
    code, out = cli(t, project, "check", "--targets", "lab,bad", "--timeout", "3s", "--trace", "--report", report, "--json")
    if code != 1 || !strings.Contains(string(out), `"completed":1`) || !strings.Contains(string(out), `"failed":1`) {
        t.Fatal(code, string(out))
    }
    data, err := os.ReadFile(report)
    if err != nil || !json.Valid(data) || strings.Contains(string(data), "127.0.0.1") || strings.Contains(string(data), project) || strings.Contains(string(data), "researcher") {
        t.Fatal(string(data), err)
    }
}
