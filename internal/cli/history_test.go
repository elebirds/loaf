package cli

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/wire"
)

type psRemote struct {
    *runRemote
    mu       sync.Mutex
    statuses map[string]string
    calls    int
}

func (r *psRemote) Observe(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (wire.Observation, error) {
    r.mu.Lock()
    r.calls++
    r.mu.Unlock()
    result, err := r.runRemote.Observe(ctx, target, runner, req, batch)
    if result.Record != nil {
        result.Record.Status = r.statuses[req.RunID]
    }
    return result, err
}

func psFixture(t *testing.T) (Options, *psRemote, []config.RunRef) {
    t.Helper()
    o, remote := runFixture(t)
    root, _, err := config.Discover(context.Background(), o.CWD)
    if err != nil {
        t.Fatal(err)
    }
    project, err := o.Store.Existing(root)
    if err != nil {
        t.Fatal(err)
    }
    observer := &psRemote{runRemote: remote, statuses: map[string]string{}}
    o.Runs.Remote = observer
    refs := []config.RunRef{}
    now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
    for i, status := range []string{"running", "failed", "succeeded", "failed"} {
        attempted := now.Add(-time.Duration(i) * time.Hour)
        ref := config.RunRef{ID: fmt.Sprintf("r_%024d", i), ProjectID: project.ID, Target: "lab", Connection: config.Target{Destination: "original", RemoteBase: "~/projects"}, RemoteDir: "/remote/project", Runner: "/runner", Version: wire.Version, CreatedAt: attempted.Add(-time.Minute), AttemptedAt: &attempted, Submission: "submitted"}
        if i == 3 {
            ref.Target = "retired"
        }
        if err := o.Store.SaveRun(context.Background(), ref); err != nil {
            t.Fatal(err)
        }
        observer.statuses[ref.ID] = status
        refs = append(refs, ref)
    }
    return o, observer, refs
}

func psResult(t *testing.T, o Options, args ...string) (int, execution.History) {
    t.Helper()
    code, out, stderr := run(t, o, append(args, "--json")...)
    var response struct {
        Data execution.History `json:"data"`
    }
    if err := json.Unmarshal([]byte(out), &response); err != nil || stderr != "" {
        t.Fatal(code, out, stderr, err)
    }
    return code, response.Data
}

func TestPSFiltersBoundedHistoryAndSavedAliases(t *testing.T) {
    o, remote, refs := psFixture(t)
    code, list := psResult(t, o, "ps", "--status", "failed", "--status", "succeeded", "--limit", "2")
    if code != 0 || list.Examined != 2 || list.Matched != 1 || !list.Truncated || list.Total != 4 || len(list.Runs) != 1 || list.Runs[0].ID != refs[1].ID || remote.calls != 2 {
        t.Fatal(code, list, remote.calls)
    }
    code, list = psResult(t, o, "ps", "--since", "2026-10-21T16:00:00+08:00", "--until", "2026-10-21T17:00:00+08:00", "--status", "failed", "--status", "succeeded")
    if code != 0 || list.Eligible != 2 || list.Matched != 2 || list.Truncated || list.Filters.Since == nil || list.Filters.Since.Location() != time.UTC || list.Runs[0].ID != refs[1].ID || list.Runs[1].ID != refs[2].ID {
        t.Fatal(code, list)
    }
    for _, args := range [][]string{{"ps", "--target", "retired"}, {"--target", "retired", "ps"}} {
        code, list = psResult(t, o, args...)
        if code != 0 || list.Eligible != 1 || list.Matched != 1 || list.Runs[0].ID != refs[3].ID {
            t.Fatal(code, list)
        }
    }
    remote.failID = refs[0].ID
    code, list = psResult(t, o, "ps", "--status", "failed")
    if code != 1 || list.Unresolved != 1 || list.Matched != 2 || len(list.Runs) != 3 || list.Runs[0].QueryError != "SSH_FAILED" || list.Runs[0].Status != "unknown" {
        t.Fatal(code, list)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "ps", "--help")
        if code != 0 || stderr != "" || !strings.Contains(out, "--status") || !strings.Contains(out, "--target") || !strings.Contains(out, "RFC3339") {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "--lang", lang, "ps", "--since", "2027-01-01T00:00:00Z")
        if code != 0 || stderr != "" || out == "" || strings.Contains(out, "<no value>") {
            t.Fatal(code, out, stderr)
        }
    }
}

func TestPSPreferencesAndInvalidCombinations(t *testing.T) {
    o, _, refs := psFixture(t)
    data, err := os.ReadFile(o.Store.ConfigFile)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(o.Store.ConfigFile, append(data, []byte("\n[ps]\nlimit = 3\n")...), 0600); err != nil {
        t.Fatal(err)
    }
    if code, list := psResult(t, o, "ps"); code != 0 || list.Examined != 3 || list.Limit != 3 {
        t.Fatal(code, list)
    }
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[ps]\nlimit = 1\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if code, list := psResult(t, o, "ps"); code != 0 || list.Examined != 1 || list.Limit != 1 {
        t.Fatal(code, list)
    }
    if code, list := psResult(t, o, "ps", "--limit", "2"); code != 0 || list.Examined != 2 || list.Limit != 2 {
        t.Fatal(code, list)
    }
    for _, args := range [][]string{
        {"ps", "--limit", "0"}, {"ps", "--status", "complete"}, {"ps", "--status", "failed,succeeded"},
        {"ps", "--since", "2026-10-21"}, {"ps", "--since", ""}, {"ps", "--until", "yesterday"},
        {"ps", "--since", "2026-10-22T00:00:00Z", "--until", "2026-10-21T00:00:00Z"},
        {"ps", "--target", ""}, {"ps", "--wait", "1s"},
    } {
        for _, lang := range []string{"en", "zh-Hans"} {
            code, out, stderr := run(t, o, append([]string{"--lang", lang, "--json"}, args...)...)
            if code != 2 || stderr != "" || !json.Valid([]byte(out)) || !strings.Contains(out, `"code":"BAD_CONFIG"`) {
                t.Fatal(args, code, out, stderr)
            }
        }
    }
    for _, flag := range []string{"--status=failed", "--since=2026-10-21T00:00:00Z", "--until=2026-10-21T00:00:00Z", "--target=lab", "--limit=1"} {
        if code, out, stderr := run(t, o, "ps", refs[0].ID, flag, "--json"); code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatal(flag, code, out, stderr)
        }
    }
    // Configured list limits do not prevent an explicit bound-run observation.
    if code, out, stderr := run(t, o, "ps", refs[0].ID, "--json"); code != 0 || stderr != "" || !strings.Contains(out, refs[0].ID) {
        t.Fatal(code, out, stderr)
    }
}
