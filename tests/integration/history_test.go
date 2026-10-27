package integration

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
)

func TestHistoryFiltersAndRemovedMachineBindings(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := t.TempDir()
    sourceFile(t, project, "loaf.toml", "[run]\nwait = '5s'\n[ps]\nlimit = 2\n")
    for _, name := range []string{"lab", "other"} {
        if code, out := cli(t, project, "add", name, "--ssh", "ssh -F "+ssh.Quote(f.config)+" fixture", "--json"); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    if code, out := cli(t, project, "use", "lab", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    success := runCLI(t, project, "run", "--json", "--", "/bin/true")
    failed := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "echo saved-log; exit 7")
    other := runCLI(t, project, "run", "--target", "other", "--json", "--", "/bin/true")
    if success.Status != "succeeded" || failed.Status != "failed" || other.Status != "succeeded" {
        t.Fatal(success, failed, other)
    }
    list := func(args ...string) (int, execution.History) {
        t.Helper()
        code, out := cli(t, project, append(append([]string{"ps"}, args...), "--json")...)
        var response struct {
            Data execution.History `json:"data"`
        }
        if err := json.Unmarshal(out, &response); err != nil {
            t.Fatal(string(out), err)
        }
        return code, response.Data
    }
    if code, data := list("--status", "failed", "--limit", "1"); code != 0 || data.Examined != 1 || data.Matched != 0 || !data.Truncated || len(data.Runs) != 0 {
        t.Fatal(code, data)
    }
    if code, data := list("--status", "failed", "--target", "lab"); code != 0 || data.Eligible != 2 || data.Matched != 1 || data.Truncated || len(data.Runs) != 1 || data.Runs[0].ID != failed.ID {
        t.Fatal(code, data)
    }
    settings := config.New(project, filepath.Join(project, "config"), filepath.Join(project, "state"))
    store := local.New(settings.StateDir)
    root, _, err := config.Discover(context.Background(), project)
    if err != nil {
        t.Fatal(err)
    }
    personal, err := store.Existing(root)
    if err != nil {
        t.Fatal(err)
    }
    refs, err := store.Runs(personal.ID)
    if err != nil || len(refs) != 3 {
        t.Fatal(refs, err)
    }
    // Time boundaries use the persisted attempt, including exact equality, not
    // client clocks or the later remote execution-created timestamp.
    boundary := refs[1].AttemptedAt.Format(time.RFC3339Nano)
    if code, data := list("--since", boundary, "--until", boundary); code != 0 || data.Eligible != 1 || data.Matched != 1 || data.Runs[0].ID != failed.ID {
        t.Fatal(code, data)
    }
    if code, out := cli(t, project, "remove", "lab", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    if code, out := cli(t, project, "targets", "--json"); code != 0 || !strings.Contains(string(out), `"target":"lab"`) || !strings.Contains(string(out), `"available":false`) {
        t.Fatal(code, string(out))
    }
    if code, out := cli(t, project, "check", "--json"); code != 2 {
        t.Fatal(code, string(out))
    }
    if kept := runCLI(t, project, "logs", "--json"); kept.ID != other.ID {
        t.Fatal(kept)
    }
    if kept := runCLI(t, project, "logs", failed.ID, "--json"); kept.ID != failed.ID || kept.Log == nil || !strings.Contains(kept.Log.Text, "saved-log") {
        t.Fatal(kept)
    }
    if code, data := list("--target", "lab"); code != 0 || data.Matched != 2 {
        t.Fatal(code, data)
    }
    // Replacing a connection must not redirect previously saved observations.
    if code, out := cli(t, project, "add", "other", "--replace", "--ssh", "ssh -F "+ssh.Quote(f.config)+" fixture-missing", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    if kept := runCLI(t, project, "ps", other.ID, "--json"); kept.Status != "succeeded" {
        t.Fatal(kept)
    }
    after, err := store.Runs(personal.ID)
    if err != nil || !reflect.DeepEqual(refs, after) {
        t.Fatal(after, err)
    }
    // An authentication failure on another saved target leaves real successful
    // observations intact, including when a status filter cannot match it yet.
    unavailable := refs[0]
    unavailable.ID = "r_ffffffffffffffffffffffff"
    unavailable.Target = "unavailable"
    attempted := time.Now().UTC()
    unavailable.AttemptedAt = &attempted
    unavailable.Connection.Destination = "missing-loaf-user@127.0.0.1"
    unavailable.Connection.Args = append(append([]string(nil), unavailable.Connection.Args...), "-p", f.port)
    if err := store.SaveRun(context.Background(), unavailable); err != nil {
        t.Fatal(err)
    }
    if code, data := list("--status", "failed", "--limit", "4"); code != 1 || data.Unresolved != 1 || data.Matched != 1 || len(data.Runs) != 2 || data.Runs[0].Status != "unknown" || data.Runs[0].QueryError == "" || data.Runs[1].ID != failed.ID {
        t.Fatal(code, data)
    }
    f.shell(`test -s "$1/runs/$2/output.log"; test -d "$1/runs/$2/work"`, failed.RemoteDir, failed.ID)
    t.Log("packaged CLI: bounded filters, inclusive attempt times, partial SSH failures, removed defaults and frozen connection bindings passed")
}
