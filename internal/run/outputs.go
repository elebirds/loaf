package run

import (
    "context"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

// InventoryRemote is the read-only metadata boundary, distinct from downloads.
type InventoryRemote interface {
    ExistingRunner(context.Context, config.Target, string, bool) error
    Inventory(context.Context, config.Target, string, wire.Request, bool) (wire.OutputInventory, error)
}

// OutputBrowser composes the explicit preparation option with read-only listing.
type OutputBrowser struct {
    Remote InventoryRemote
    Runner func(context.Context, config.Target, string, bool) (string, error)
}

// OutputListing binds public metadata to one saved run and frozen selection.
type OutputListing struct {
    RunID  string   `json:"run_id"`
    Target string   `json:"target"`
    Paths  []string `json:"paths"`
    wire.OutputInventory
}

// List does not prepare a helper unless explicitly authorized for this call.
func (s OutputBrowser) List(ctx context.Context, ref task.RunRef, depth, limit int, prepare, batch bool) (result OutputListing, err error) {
    result = OutputListing{RunID: ref.ID, Target: ref.Target, Paths: append([]string(nil), ref.Outputs...)}
    if depth < 0 || depth > 20 || limit < 1 || limit > 1000 {
        return result, fault.Invalid("--depth/--limit", "out_of_range", "outputs_bounds")
    }
    if err := AvailabilityError(ref); err != nil {
        return result, err
    }
    paths, err := sources.OutputPaths(ref.Outputs)
    if err != nil {
        return result, err
    }
    query, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    runner := ref.Runner
    if prepare {
        if s.Runner == nil {
            return result, fault.New("BUILD_REQUIRED", "runner_missing", nil)
        }
        runner, err = s.Runner(query, ref.Connection, "", batch)
    } else {
        err = s.Remote.ExistingRunner(query, ref.Connection, runner, batch)
    }
    if err != nil {
        return result, err
    }
    result.OutputInventory, err = s.Remote.Inventory(query, ref.Connection, runner, wire.Request{FormatVersion: wire.FormatVersion, Version: ref.Version, Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, Outputs: paths, Depth: depth, Limit: limit}, batch)
    if err == nil {
        inventory := result.OutputInventory
        if len(inventory.Entries) > limit || inventory.Examined != len(inventory.Entries) || inventory.RootsTotal != len(paths) || inventory.RootsExamined > len(paths) || inventory.Depth != depth || inventory.Limit != limit {
            return result, fault.New("BAD_RESPONSE", "pull_format", nil)
        }
        for _, entry := range inventory.Entries {
            valid := false
            for _, root := range paths {
                if entry.Path == root || strings.HasPrefix(entry.Path, root+"/") {
                    valid = true
                }
            }
            if !wire.ValidOutputPath(entry.Path) || !valid {
                return result, fault.New("BAD_RESPONSE", "pull_format", nil)
            }
        }
    }
    if err == nil && len(result.Missing) > 0 {
        err = fault.New("OUTPUT_MISSING", "pull_missing", nil)
    }
    return result, err
}
