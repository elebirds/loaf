package cli

import (
    "context"
    "encoding/json"
    "errors"
    "path/filepath"
    "strings"
    "sync"
    "testing"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

type batchPuller struct {
    mu           sync.Mutex
    destinations map[string]string
    fail         string
}

func (p *batchPuller) Pull(_ context.Context, ref task.RunRef, destination string, paths []string, policy config.PullPolicy, _ bool) (sources.PullResult, error) {
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
    puller := &batchPuller{destinations: map[string]string{}}
    o.Puller = puller
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    app := client.New(client.Dependencies{Config: o.Config, State: o.State, Runs: o.Runs, Puller: puller, CWD: o.CWD}, config.Global{})
    into := "comparison"
    call, err := app.Projects.Pull(ctx, client.PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: &into, Policy: client.CallPolicy{Batch: true}}, nil)
    result := call.Batch
    if result == nil {
        t.Fatal(err)
    }
    if err == nil || result.Unexamined != 2 || result.Completed != 0 || len(puller.destinations) != 0 {
        t.Fatal(result, err)
    }
    for _, row := range result.Runs {
        if row.Error == nil || !errors.As(row.Error, new(*fault.Error)) {
            t.Fatal(row)
        }
    }
}
