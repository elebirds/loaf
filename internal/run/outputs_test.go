package run

import (
    "context"
    "errors"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

type inventoryRemote struct {
    checks, calls, prepared int
    fail                    bool
    req                     wire.Request
}

func (r *inventoryRemote) ExistingRunner(context.Context, config.Target, string, bool) error {
    r.checks++
    if r.fail {
        return fault.New("RUNNER_REQUIRED", "outputs_runner_required", nil)
    }
    return nil
}
func (r *inventoryRemote) Inventory(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.OutputInventory, error) {
    r.calls++
    r.req = req
    return wire.OutputInventory{Entries: []wire.OutputEntry{}, Missing: []string{}, Depth: req.Depth, Limit: req.Limit, RootsExamined: len(req.Outputs), RootsTotal: len(req.Outputs)}, nil
}
func TestOutputBrowserDoesNotPrepareWithoutExplicitAuthorization(t *testing.T) {
    r := &inventoryRemote{fail: true}
    s := OutputBrowser{Remote: r, Runner: func(context.Context, config.Target, string, bool) (string, error) {
        r.prepared++
        return "/current", nil
    }}
    ref := historyRefs()[0]
    ref.Outputs = []string{"saved"}
    _, err := s.List(context.Background(), ref, 2, 200, false, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "RUNNER_REQUIRED" || r.calls != 0 || r.prepared != 0 {
        t.Fatal(r, err)
    }
    result, err := s.List(context.Background(), ref, 2, 200, true, true)
    if err != nil || r.prepared != 1 || r.calls != 1 || r.checks != 1 || len(result.Paths) != 1 || r.req.Outputs[0] != "saved" {
        t.Fatal(result, r, err)
    }
}
