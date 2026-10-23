package ssh

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// Clean sends one destructive request to the separately deployed current helper.
// A transport failure is not proof that deletion failed or completed.
func (c *Client) Clean(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (receipt *wire.CleanupReceipt, err error) {
    defer func() { err = fault.At(err, "clean") }()
    if req.Version != wire.Version {
        return nil, cleanVersionError(req.Version)
    }
    req.Action = "clean"
    data, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }
    out, transportErr := c.Execute(ctx, target, Quote(runner), bytes.NewReader(data), batch)
    var response wire.Response
    if json.Unmarshal(out, &response) != nil {
        if transportErr != nil {
            return nil, transportErr
        }
        return nil, errors.New("BAD_RESPONSE")
    }
    if response.FormatVersion != wire.FormatVersion {
        return nil, errors.New("BAD_FORMAT")
    }
    if response.Version != wire.Version {
        return nil, cleanVersionError(response.Version)
    }
    if !response.OK {
        if response.Error == nil {
            return response.Cleanup, errors.New("BAD_RESPONSE")
        }
        return response.Cleanup, &RemoteError{response.Error.Code}
    }
    if transportErr != nil {
        return response.Cleanup, transportErr
    }
    if response.Cleanup == nil {
        return nil, errors.New("BAD_RESPONSE")
    }
    return response.Cleanup, nil
}

func cleanVersionError(actual string) *fault.Error {
    f := fault.New("VERSION_MISMATCH", "runner_version_mismatch", map[string]any{"Expected": wire.Version, "Actual": actual})
    f.Hint = "runner_version_hint"
    return f
}
