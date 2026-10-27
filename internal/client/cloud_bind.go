package client

import (
    "context"
    "io"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

func (c *Resources) binding(in CloudSelect, id string) (cloud.Binding, error) {
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
        name, a, err := c.selectAccount(in)
        if err != nil {
            return b, err
        }
        b = cloud.Binding{Ref: cloud.Ref{Provider: a.Provider, Product: selectedProduct(in, a, ""), Account: name, ID: id}, Settings: a}
    }
    if !config.ValidCloudBinding(b) {
        return b, cloudConfigError()
    }
    if c.Cloud == nil {
        return b, fault.New("CLOUD_FAILED", "cloud_failed", nil)
    }
    return b, nil
}

// CloudBind recovers an acquisition or attaches an explicitly selected instance.
func (c *Resources) CloudBind(ctx context.Context, in CloudBindInput) (CloudBinding, error) {
    if (in.Instance == "") == (in.Request == "") || !config.ValidName(in.Name) || in.ReadyTimeout < 0 || in.ReadyTimeout > 10*time.Minute {
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
    if !config.ValidRemotePath(remote) {
        return CloudBinding{}, cloudConfigError()
    }
    result, err := c.bindCloud(ctx, in.Name, b, remote, in.Replace, in.ReadyTimeout, saved.Plan.Clone)
    if err == nil && in.Request != "" {
        err = c.Cloud.MarkReady(ctx, in.Request, b.Ref)
    }
    return result, err
}

func (c *Resources) bindCloud(ctx context.Context, name string, b cloud.Binding, remote string, replace bool, ready time.Duration, clone *cloud.CloneSource) (CloudBinding, error) {
    result := CloudBinding{Target: name, Instance: b.Ref, Status: "not_bound"}
    if !config.ValidCloudBinding(b) {
        return result, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    target := config.Target{Cloud: &b, RemoteBase: remote}
    if replace {
        current, err := c.Config.Load()
        if err != nil {
            return result, err
        }
        if saved, ok := current.Targets[name]; ok && saved.Cloud != nil && *saved.Cloud == b {
            target.Args = append([]string{}, saved.Args...)
        }
    }
    if c.Operations == nil {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    deadline, cancel := context.WithTimeout(ctx, max(ready, 20*time.Second))
    defer cancel()
    p, err := c.Cloud.Open(deadline, b.Ref.Account, b.Settings, b.Ref.Product)
    if err != nil {
        return result, cloud.Failure(err)
    }
    for {
        observed, err := p.Get(deadline, b.Ref)
        if err != nil {
            return result, cloud.Failure(err)
        }
        if clone != nil {
            result.CloneDataStatus = observed.CloneDataStatus
            if b.Ref == clone.Instance || observed.Region != clone.Region {
                return result, fault.New("BAD_CONFIG", "cloud_clone_region", nil)
            }
        }
        if result.Status == "not_bound" {
            if err := c.Config.Add(ctx, name, target, replace); err != nil {
                return result, err
            }
            result.Status = "bound"
        }
        if clone != nil && observed.CloneDataStatus == "failed" {
            return result, fault.New("CLONE_FAILED", "cloud_clone_failed", map[string]any{"ID": b.Ref.ID})
        }
        if err == nil && observed.Status == "running" && (clone == nil || observed.CloneDataStatus == "succeeded") {
            _, err = c.Operations.Exec(deadline, target, ssh.ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
            if err == nil {
                result.Status = "ready"
                return result, nil
            }
            // Unknown host/authentication needs human action, not a 2m retry loop.
            f := fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", err)
            f.Hint = "cloud_connect_hint"
            return result, f
        }
        if ready == 0 || deadline.Err() != nil {
            if clone != nil {
                f := fault.New("CLOUD_NOT_READY", "cloud_clone_not_ready", nil)
                f.Hint = "cloud_clone_wait_hint"
                return result, f
            }
            f := fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", err)
            f.Hint = "cloud_connect_hint"
            return result, f
        }
        select {
        case <-deadline.Done():
            if clone != nil {
                f := fault.New("CLOUD_NOT_READY", "cloud_clone_not_ready", nil)
                f.Hint = "cloud_clone_wait_hint"
                return result, f
            }
            return result, fault.New("CLOUD_NOT_READY", "cloud_not_ready", nil)
        case <-time.After(10 * time.Second):
        }
    }
}
