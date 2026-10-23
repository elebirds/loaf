package ssh

import (
    "context"
    "encoding/json"
    "errors"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// ExistingRunner verifies only the saved current executable, without uploading.
func (c *Client) ExistingRunner(ctx context.Context, target config.Target, runner string, batch bool) error {
    if runner == "" {
        return runnerRequired(nil)
    }
    out, err := c.executeRead(ctx, target, "if test -f "+Quote(runner)+" && test -x "+Quote(runner)+"; then printf ready; else printf missing; fi", nil, batch)
    if err != nil {
        return err
    }
    if string(out) != "ready" {
        return runnerRequired(nil)
    }
    _, err = c.Control(ctx, target, runner, wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "version"}, batch)
    if err != nil {
        var remote *RemoteError
        if errors.As(err, &remote) || err.Error() == "BAD_RESPONSE" {
            return runnerRequired(err)
        }
    }
    return err
}
func runnerRequired(cause error) error {
    f := fault.Wrap("RUNNER_REQUIRED", "outputs_runner_required", cause)
    f.Hint = "outputs_prepare_hint"
    return f
}

// Inventory requests bounded metadata from an already verified helper.
func (c *Client) Inventory(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (result wire.OutputInventory, err error) {
    finish := c.Trace.stage("inventory")
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, "inventory") }()
    req.Action = "inventory"
    data, err := json.Marshal(req)
    if err != nil {
        return result, err
    }
    out, transportErr := c.executeRead(ctx, target, Quote(runner), data, batch)
    var response wire.Response
    if json.Unmarshal(out, &response) != nil {
        if transportErr != nil {
            return result, transportErr
        }
        return result, pullFailure("BAD_RESPONSE", nil, "")
    }
    if response.FormatVersion != wire.FormatVersion {
        return result, pullFailure("BAD_FORMAT", nil, "")
    }
    if response.Version != wire.Version || req.Version != wire.Version {
        return result, pullFailure("VERSION_MISMATCH", nil, "")
    }
    if response.Inventory != nil {
        result = *response.Inventory
    }
    if !response.OK {
        if response.Error == nil {
            return result, pullFailure("BAD_RESPONSE", nil, "")
        }
        return result, pullFailure(response.Error.Code, transportErr, "")
    }
    if transportErr != nil {
        return result, transportErr
    }
    if response.Inventory == nil {
        return result, pullFailure("BAD_RESPONSE", nil, "")
    }
    return result, nil
}
