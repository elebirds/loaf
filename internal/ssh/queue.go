package ssh

import (
    "context"
    "errors"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/queue"
    "github.com/elebirds/loaf/internal/wire"
)

// QueueControl transports one private admission operation. The shared finite
// transport classifies read recovery; mutations are never automatically replayed.
func (c *Client) QueueControl(ctx context.Context, target config.Target, runner string, request wire.Request, batch bool) (queue.Result, error) {
    request.Action = "queue"
    response, err := c.ControlResponse(ctx, target, runner, request, batch)
    if response.Queue == nil {
        return queue.Result{}, errOrBadResponse(err)
    }
    return *response.Queue, err
}

func errOrBadResponse(err error) error {
    if err != nil {
        return err
    }
    return errors.New("BAD_RESPONSE")
}
