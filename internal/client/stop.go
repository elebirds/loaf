package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

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
