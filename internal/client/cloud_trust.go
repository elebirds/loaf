package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

// ConfirmCloudTrust delegates the fingerprint decision to native OpenSSH.
// The authentication-free handshake never establishes resource readiness.
func (c *Resources) ConfirmCloudTrust(ctx context.Context, binding CloudBinding, streams Streams) error {
    settings, err := c.Config.Load()
    if err != nil {
        return err
    }
    target, ok := settings.Targets[binding.Target]
    if !ok || target.Cloud == nil || target.Cloud.Ref != binding.Instance {
        return fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    if binding.principal.Ref.ID != "" && *target.Cloud != binding.principal {
        return fault.New("BAD_CONFIG", "cloud_account_mismatch", nil)
    }
    if binding.Request != nil && target.Cloud.Settings != binding.Request.Settings {
        return fault.New("BAD_CONFIG", "cloud_account_mismatch", nil)
    }
    if c.Operations == nil {
        return fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    _, err = c.Operations.Exec(ctx, target, ssh.ExecInput{Args: []string{"/bin/true"}, TrustOnly: true}, streams.In, streams.Out, streams.Err)
    // An authentication-free handshake normally ends with permission denied.
    // A separate strict authenticated probe determines readiness afterwards.
    if ssh.FailureKind(err) == "authentication" {
        return nil
    }
    return err
}

// RetryCloudBinding observes the exact retained resource without allocating.
func (c *Resources) RetryCloudBinding(ctx context.Context, binding CloudBinding, input CloudBindInput) (CloudBinding, error) {
    if input.ReadyTimeout < 0 || input.ReadyTimeout > 10*time.Minute {
        return binding, cloudConfigError()
    }
    input.Name = binding.Target
    if binding.Request != nil {
        input.Instance, input.Request = "", binding.Request.ID
    } else {
        settings, err := c.Config.Load()
        if err != nil {
            return binding, err
        }
        target, ok := settings.Targets[binding.Target]
        if !ok && binding.Status == "not_bound" && config.ValidCloudBinding(binding.principal) {
            return c.bindCloud(ctx, binding.Target, binding.principal, binding.remoteBase, false, input.ReadyTimeout, nil, input.Progress)
        }
        if !ok || target.Cloud == nil || target.Cloud.Ref != binding.Instance || binding.principal.Ref.ID != "" && *target.Cloud != binding.principal {
            return binding, fault.New("BAD_CONFIG", "cloud_account_mismatch", nil)
        }
        c.Settings = settings
        input.Instance, input.Request = binding.Target, ""
    }
    input.Select = CloudSelect{Account: &binding.Instance.Account, Product: &binding.Instance.Product}
    return c.CloudBind(ctx, input)
}

// ObserveCloudRequest continues a saved power operation without replaying it.
func (c *Resources) ObserveCloudRequest(ctx context.Context, id string) (cloud.Receipt, error) {
    return c.CloudReconcile(ctx, CloudReconcileInput{Request: id}, nil)
}

// CloudBindingCredentials reads the retained resource even if platform inspection
// failed before the alias could be saved. Current convenience defaults are not used.
func (c *Resources) CloudBindingCredentials(ctx context.Context, binding CloudBinding) (CloudCredentials, error) {
    principal := binding.principal
    if binding.Request != nil && binding.Request.Instance != nil {
        principal = cloud.Binding{Ref: *binding.Request.Instance, Settings: binding.Request.Settings}
    }
    if !config.ValidCloudBinding(principal) || principal.Ref != binding.Instance {
        return CloudCredentials{}, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    return c.cloudCredentials(ctx, principal, false)
}
