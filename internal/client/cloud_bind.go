package client

import (
    "context"
    "errors"
    "io"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

func (c *Resources) binding(in CloudSelect, id string) (cloud.Binding, error) {
    b, err := c.localBinding(in, id)
    if err == nil && c.Cloud == nil {
        err = fault.New("CLOUD_FAILED", "cloud_failed", nil)
    }
    return b, err
}

// localBinding resolves frozen settings without opening providers or credentials.
func (c *Resources) localBinding(in CloudSelect, id string) (cloud.Binding, error) {
    var b cloud.Binding
    if target, ok := c.Settings.Targets[id]; ok && target.Cloud != nil {
        b = *target.Cloud
        if in.Account != nil && *in.Account != b.Ref.Account {
            return b, fault.New("BAD_CONFIG", "cloud_account_mismatch", nil)
        }
        if in.Product != nil && *in.Product != b.Ref.Product {
            return b, fault.New("BAD_CONFIG", "cloud_product_mismatch", nil)
        }
    } else {
        name := c.Settings.Cloud.DefaultAccount
        if in.Account != nil {
            name = *in.Account
        }
        a, ok := c.Settings.Cloud.Accounts[name]
        if !ok {
            return b, fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
        }
        b = cloud.Binding{Ref: cloud.Ref{Provider: a.Provider, Product: selectedProduct(in, a, ""), Account: name, ID: id}, Settings: a}
    }
    if !config.ValidCloudBinding(b) {
        return b, cloudConfigError()
    }
    return b, nil
}

// CloudBind recovers an acquisition or attaches an explicitly selected instance.
func (c *Resources) CloudBind(ctx context.Context, in CloudBindInput) (CloudBinding, error) {
    if (in.Instance == "") == (in.Request == "") || (in.Name != "" && !config.ValidName(in.Name)) || (in.Name == "" && in.Request == "") || in.ReadyTimeout < 0 || in.ReadyTimeout > 10*time.Minute {
        return CloudBinding{}, cloudConfigError()
    }
    remote := "~/.local/share/loaf/projects"
    if in.RemoteBase != nil {
        remote = *in.RemoteBase
    }
    var b cloud.Binding
    var saved cloud.Receipt
    var err error
    if in.Request != "" {
        saved, err = c.CloudRequest(ctx, in.Select, in.Request)
        if err != nil {
            return CloudBinding{}, err
        }
        if in.Name == "" {
            in.Name = saved.Target
        }
        if saved.Instance == nil || saved.Operation != "acquire" || saved.Target != in.Name {
            return CloudBinding{}, cloudConfigError()
        }
        b = cloud.Binding{Ref: *saved.Instance, Settings: saved.Settings}
        if in.RemoteBase == nil {
            remote = saved.RemoteBase
        }
        if target, ok := c.Settings.Targets[in.Name]; ok && target.Cloud != nil && *target.Cloud == b {
            in.Replace = true
        }
    } else {
        b, err = c.binding(in.Select, in.Instance)
        if err != nil {
            return CloudBinding{}, err
        }
    }
    if in.RemoteBase == nil && in.Request == "" {
        if saved, ok := c.Settings.Targets[in.Name]; ok && saved.Cloud != nil && *saved.Cloud == b {
            remote = saved.RemoteBase
        }
    }
    if !config.ValidRemotePath(remote) {
        return CloudBinding{}, cloudConfigError()
    }
    result, err := c.bindCloud(ctx, in.Name, b, remote, in.Replace, in.ReadyTimeout, saved.Plan.Clone, in.Progress)
    if in.Request != "" {
        result.Request = &saved
    }
    if err == nil && in.Request != "" {
        err = c.Cloud.MarkReady(ctx, in.Request, b.Ref)
        if err == nil {
            result.Request.Status = "ready"
        }
    }
    return result, err
}

func (c *Resources) bindCloud(ctx context.Context, name string, b cloud.Binding, remote string, replace bool, ready time.Duration, clone *cloud.CloneSource, progress ...func(cloud.Event) error) (CloudBinding, error) {
    result := CloudBinding{Target: name, Instance: b.Ref, Status: "not_bound", principal: b, remoteBase: remote}
    if !config.ValidCloudBinding(b) {
        return result, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    target := config.Target{Cloud: &b, RemoteBase: remote}
    current, err := c.Config.Load()
    if err != nil {
        return result, err
    }
    if saved, ok := current.Targets[name]; ok {
        if saved.Cloud != nil && *saved.Cloud == b {
            target.Args = append([]string{}, saved.Args...)
            replace = true
        } else if !replace {
            return result, fault.New("BAD_CONFIG", "target_exists", map[string]any{"Target": name})
        }
    }
    if c.Operations == nil {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    window := ready
    if window == 0 {
        window = 20 * time.Second
    }
    deadline, cancel := context.WithTimeout(ctx, window)
    defer cancel()
    var provider cloud.Provider
    probes := 0
    started := time.Now()
    emit := func(stage string, failure error, next *time.Time) error {
        if len(progress) == 0 || progress[0] == nil {
            return nil
        }
        return progress[0](cloud.Event{Type: stage, At: time.Now().UTC(), Instance: &b.Ref, Target: name, ProbeAttempts: probes, ElapsedMS: time.Since(started).Milliseconds(), FailureKind: ssh.FailureKind(failure), FailureReason: ssh.FailureReason(failure), NextCheckAt: next})
    }
    for {
        if ctx.Err() != nil {
            return result, ctx.Err()
        }
        var observed cloud.Instance
        var failure error
        if provider == nil {
            provider, failure = c.Cloud.Open(deadline, b.Ref.Account, b.Settings, b.Ref.Product)
            failure = cloud.Failure(failure)
        }
        if failure == nil {
            observed, failure = provider.Get(deadline, b.Ref)
            failure = cloud.Failure(failure)
        }
        if ctx.Err() != nil {
            return result, ctx.Err()
        }
        if failure != nil && !retryableReadiness(failure) {
            return result, failure
        }
        if failure == nil {
            if clone != nil {
                result.CloneDataStatus = observed.CloneDataStatus
                if b.Ref == clone.Instance || observed.Region != clone.Region {
                    return result, fault.New("BAD_CONFIG", "cloud_clone_region", nil)
                }
                if observed.CloneDataStatus == "failed" {
                    return result, fault.New("CLONE_FAILED", "cloud_clone_failed", map[string]any{"ID": b.Ref.ID})
                }
            }
            if result.Status == "not_bound" {
                if err := c.Config.Add(ctx, name, target, replace); err != nil {
                    return result, err
                }
                result.Status = "bound"
                if c.Settings.Targets == nil {
                    c.Settings.Targets = map[string]config.Target{}
                }
                c.Settings.Targets[name] = target
            }
        }
        stage := "starting"
        if clone != nil && observed.CloneDataStatus != "succeeded" {
            stage = "copying"
        }
        if e := emit(stage, failure, nil); e != nil {
            return result, e
        }
        if failure == nil && observed.Status == "running" && (clone == nil || observed.CloneDataStatus == "succeeded") {
            stage = "connecting"
            probes++
            if e := emit(stage, nil, nil); e != nil {
                return result, e
            }
            probe, stop := context.WithTimeout(deadline, 10*time.Second)
            _, failure = c.Operations.Exec(probe, target, ssh.ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
            retry := retryableReadiness(failure) || errors.Is(probe.Err(), context.DeadlineExceeded) && deadline.Err() == nil
            stop()
            if ctx.Err() != nil {
                return result, ctx.Err()
            }
            if failure == nil {
                result.Status = "ready"
                return result, nil
            }
            if !retry {
                return result, readinessFailure(failure)
            }
        }
        if ready == 0 || deadline.Err() != nil {
            if clone != nil {
                return result, cloneReadinessFailure(failure)
            }
            return result, readinessFailure(failure)
        }
        next := time.Now().Add(5 * time.Second).UTC()
        if e := emit(stage, failure, &next); e != nil {
            return result, e
        }
        if e := c.waitReady(deadline, 5*time.Second); e != nil {
            if ctx.Err() != nil {
                return result, ctx.Err()
            }
            if clone != nil {
                return result, cloneReadinessFailure(failure)
            }
            return result, readinessFailure(failure)
        }
    }
}

func retryableReadiness(err error) bool {
    var api *cloud.APIError
    return errors.As(err, &api) && api.Transient || ssh.RetryableConnection(err)
}

func readinessFailure(err error) error {
    f := fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", err)
    f.Hint = "cloud_connect_hint"
    switch ssh.FailureKind(err) {
    case "host_trust":
        f.Hint = "cloud_connect_trust_hint"
        if ssh.FailureReason(err) == "host_key_changed" {
            f.Hint = "cloud_connect_changed_hint"
        }
    case "authentication":
        f.Hint = "cloud_connect_auth_hint"
    }
    return fault.At(f, "cloud_readiness")
}

func cloneReadinessFailure(err error) error {
    f := fault.Wrap("CLOUD_NOT_READY", "cloud_clone_not_ready", err)
    f.Hint = "cloud_clone_wait_hint"
    return fault.At(f, "cloud_readiness")
}

func (c *Resources) waitReady(ctx context.Context, delay time.Duration) error {
    if c.ReadyWait != nil {
        return c.ReadyWait(ctx, delay)
    }
    timer := time.NewTimer(delay)
    defer timer.Stop()
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
        return nil
    }
}
