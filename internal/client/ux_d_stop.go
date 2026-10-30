package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/wire"
)

// StopReview contains the saved identity and effective grace for an explicit
// menu confirmation. It does not query a machine or prove that a run is active.
type StopReview struct {
    Run   execution.LocalDetails
    Grace time.Duration
}

// PrepareStop reads local facts only. Unlike Stop, this new review entry point
// requires an explicit ID; the existing direct Stop keeps its latest semantics.
func (c *Projects) PrepareStop(ctx context.Context, in StopInput) (StopReview, error) {
    if in.ID == "" {
        return StopReview{}, fault.Invalid("run-id", "missing_argument", "run_not_found")
    }
    project, grace, err := c.stopContext(ctx, in)
    if err != nil {
        return StopReview{}, err
    }
    service := execution.Service{Index: c.State, Metadata: c.State, Observations: c.State}
    detail, err := service.Inspect(project, in.ID)
    if err != nil {
        return StopReview{}, err
    }
    review := StopReview{Run: detail, Grace: wire.DefaultStopGrace}
    if grace != nil {
        review.Grace = *grace
    }
    return review, nil
}

func (c *Projects) stopContext(ctx context.Context, in StopInput) (config.Project, *time.Duration, error) {
    project, file, err := c.boundProject(ctx)
    if err != nil {
        return project, nil, err
    }
    layers := []config.StopSettings{c.Settings.Stop, file.Stop}
    if in.Grace != nil {
        if *in.Grace == "" {
            return project, nil, fault.New("BAD_CONFIG", "invalid_stop_grace", nil)
        }
        layers = append(layers, config.StopSettings{Grace: *in.Grace})
    }
    grace, err := config.ResolveStopGrace(layers...)
    return project, grace, err
}
