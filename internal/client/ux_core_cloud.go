package client

import (
    "context"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// CloudBoundInfo observes a previously selected frozen binding. Mutable account
// defaults or a later alias replacement cannot redirect this read-only action.
func (c *Resources) CloudBoundInfo(ctx context.Context, binding cloud.Binding) (cloud.Instance, error) {
    if !config.ValidCloudBinding(binding) {
        return cloud.Instance{}, cloudConfigError()
    }
    if c.Cloud == nil {
        return cloud.Instance{}, fault.New("CLOUD_FAILED", "cloud_failed", nil)
    }
    return c.cloudInfo(ctx, binding)
}
