package client

import (
    "context"
    "errors"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
)

// RunRequirements identifies only locally missing inputs. It does not replace
// Run's full validation, select sources, allocate identities or contact a host.
type RunRequirements struct {
    CommandMissing bool
    TargetMissing  bool
    Target         string
}

// RunRequirements lets an interactive caller fill gaps without making that
// preparation a prerequisite for existing Run or Submit callers.
func (c *Projects) RunRequirements(ctx context.Context, in RunInput) (RunRequirements, error) {
    root, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return RunRequirements{}, err
    }
    result := RunRequirements{CommandMissing: len(in.Args) == 0 && strings.TrimSpace(file.Run.Command) == ""}
    if !result.CommandMissing {
        if _, err := execution.ResolveExecution(file, in.Args); err != nil {
            return result, err
        }
    }
    project, err := c.State.Existing(root)
    if err != nil {
        return result, err
    }
    result.Target, _, err = config.Resolve(value(in.Target), project, file, c.Settings)
    var failure *fault.Error
    if in.Target == nil && errors.As(err, &failure) && (failure.Message == "target_required" || failure.Message == "target_unknown") {
        result.TargetMissing = true
        return result, nil
    }
    return result, err
}

// SyncTarget resolves local destination settings without broadening the
// target-free Sync dry-run contract or creating project state.
func (c *Projects) SyncTarget(ctx context.Context, target *string) (string, error) {
    root, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return "", err
    }
    project, err := c.State.Existing(root)
    if err != nil {
        return "", err
    }
    name, _, err := config.Resolve(value(target), project, file, c.Settings)
    return name, err
}
