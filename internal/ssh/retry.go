package ssh

import (
    "bytes"
    "context"
    "errors"
    "math/rand/v2"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// retryReadTrace allows one bounded recovery of a transient read failure. Callers
// must not use it around launch, stop, cleanup, uploads, or foreground commands.
func retryReadTrace(ctx context.Context, trace *Trace, read func() error) error {
    err := read()
    if ctx.Err() != nil || !transientReadFailure(err) {
        return err
    }
    delay := 200*time.Millisecond + time.Duration(rand.IntN(200))*time.Millisecond
    var api *cloud.APIError
    if errors.As(err, &api) && api.RetryAfter > delay {
        // A foreground read must not violate a provider's retry window or turn
        // a short recovery into an unexpectedly long polling operation.
        if api.RetryAfter > 2*time.Second {
            return err
        }
        delay = api.RetryAfter
    }
    timer := time.NewTimer(delay)
    defer timer.Stop()
    select {
    case <-ctx.Done():
        return err
    case <-timer.C:
        trace.retried(err)
        return read()
    }
}

// IsTransientReadError identifies the transport failures eligible for bounded,
// explicitly requested cursor recovery. It never authorizes mutation replay.
func IsTransientReadError(err error) bool { return transientReadFailure(err) }

func transientReadFailure(err error) bool {
    var f *fault.Error
    if !errors.As(err, &f) {
        return false
    }
    if f.Code == "CLOUD_FAILED" {
        return f.Params["ProviderCode"] == "TRANSPORT" || f.Params["HTTPStatus"] == 429 || f.Params["HTTPStatus"] == 502 || f.Params["HTTPStatus"] == 503 || f.Params["HTTPStatus"] == 504
    }
    if f.Code != "SSH_FAILED" && f.Code != "PULL_FAILED" {
        return false
    }
    detail, _ := f.Params["Detail"].(string)
    detail = strings.ToLower(detail)
    // Authentication, trust, remote path and configuration errors are not
    // transient. In particular, do not retry every SSH exit 255 indiscriminately.
    for _, text := range []string{"permission denied", "host key verification failed", "remote host identification has changed", "bad configuration", "no such file or directory"} {
        if strings.Contains(detail, text) {
            return false
        }
    }
    var scoped *scopedTransportError
    if errors.As(err, &scoped) && scoped.disconnected() {
        return true
    }
    for _, text := range []string{"connection closed", "connection reset", "connection timed out", "operation timed out", "connection refused", "broken pipe", "unexpected end of file"} {
        if strings.Contains(detail, text) {
            return true
        }
    }
    return false
}

func (c *Client) executeRead(ctx context.Context, target config.Target, command string, input []byte, batch bool) ([]byte, error) {
    var out []byte
    err := retryReadTrace(ctx, c.Trace, func() (err error) {
        next, err := c.Execute(ctx, target, command, bytes.NewReader(input), batch)
        // A response may have arrived before SSH lost its final confirmation.
        // Do not erase that observation if reconnecting returns no data at all.
        if err == nil || len(next) > 0 {
            out = next
        }
        return err
    })
    return out, err
}
