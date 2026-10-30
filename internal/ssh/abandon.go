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

// Abandon sends one explicit preparation fence or a read-only preview. A lost
// response never causes the transport to replay the mutation.
func (c *Client) Abandon(ctx context.Context, target config.Target, runner string, req wire.Request, dry, batch bool) (receipt *wire.AbandonReceipt, err error) {
    defer func() { err = fault.At(err, "abandon") }()
    if req.Version != wire.Version {
        return nil, cleanVersionError(req.Version)
    }
    req.Action = "abandon"
    if dry {
        req.Action = "abandon-preview"
    }
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
            return response.Abandonment, errors.New("BAD_RESPONSE")
        }
        return response.Abandonment, &RemoteError{response.Error.Code}
    }
    if transportErr != nil {
        return response.Abandonment, transportErr
    }
    if response.Abandonment == nil {
        return nil, errors.New("BAD_RESPONSE")
    }
    return response.Abandonment, nil
}
