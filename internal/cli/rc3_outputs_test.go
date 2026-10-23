package cli

import (
    "context"
    "encoding/json"
    "github.com/elebirds/loaf/internal/i18n"
    "path/filepath"
    "strings"
    "sync"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
)

type batchPuller struct {
    mu           sync.Mutex
    destinations map[string]string
    fail         string
}

func (p *batchPuller) Pull(_ context.Context, ref config.RunRef, destination string, paths []string, policy config.PullPolicy, _ bool) (sources.PullResult, error) {
    p.mu.Lock()
    p.destinations[ref.ID] = destination
    p.mu.Unlock()
    result := sources.PullResult{RunID: ref.ID, Target: ref.Target, Destination: destination, Paths: paths, Policy: policy}
    if ref.ID == p.fail {
        return result, fault.New("OUTPUT_MISSING", "pull_missing", nil)
    }
    return result, nil
}
func TestBatchPullIsolatesDestinationsAndRetainsPartialResults(t *testing.T) {
    o, _, refs := psFixture(t)
    for i := range refs {
        refs[i].Outputs = []string{"outputs"}
        if err := o.Store.SaveRun(context.Background(), refs[i]); err != nil {
            t.Fatal(err)
        }
    }
    p := &batchPuller{destinations: map[string]string{}, fail: refs[0].ID}
    o.Puller = p
    code, out, stderr := run(t, o, "pull", refs[0].ID, refs[1].ID, "--into", "comparison", "--concurrency", "2", "--json")
    if code != 1 || stderr != "" || strings.Contains(out, `"message":""`) || !json.Valid([]byte(out)) || !strings.Contains(out, `"completed":1`) || !strings.Contains(out, `"failed":1`) {
        t.Fatal(code, out, stderr)
    }
    for _, ref := range refs[:2] {
        if filepath.Base(p.destinations[ref.ID]) != ref.ID || filepath.Base(filepath.Dir(p.destinations[ref.ID])) != "comparison" {
            t.Fatal(p.destinations)
        }
    }
    for _, args := range [][]string{{"pull", refs[0].ID, refs[1].ID, "--into", "x", "--to", "y"}, {"pull", refs[0].ID, "--concurrency", "2"}, {"pull", "--into", "x"}, {"pull", refs[0].ID, refs[0].ID, "--into", "x"}} {
        code, out, stderr = run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" {
            t.Fatal(code, out, stderr)
        }
    }
}

func TestCanceledBatchPullDoesNotClaimUnexaminedRowsSucceeded(t *testing.T) {
    o, _, refs := psFixture(t)
    for i := range refs {
        refs[i].Outputs = []string{"outputs"}
    }
    puller := &batchPuller{destinations: map[string]string{}}
    o.Puller = puller
    project, err := o.Store.Existing(o.CWD)
    if err != nil {
        t.Fatal(err)
    }
    project.Root = o.CWD
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    l, _ := i18n.New("en", nil)
    result, err := batchPull(ctx, o, project, refs[:2], "comparison", nil, false, config.PullPolicy{}, 1, true, l)
    if err == nil || result.Unexamined != 2 || result.Completed != 0 || len(puller.destinations) != 0 {
        t.Fatal(result, err)
    }
    for _, row := range result.Runs {
        if row.Error == nil || row.Error.Code != "NOT_EXAMINED" || row.Error.Message == "" {
            t.Fatal(row)
        }
    }
}
