package ssh

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// RemoteError distinguishes a runner's response from an interrupted transport.
type RemoteError struct{ Code string }

func (e *RemoteError) Error() string { return e.Code }

// Observe returns a finite remote observation, optionally after bounded waiting.
func (c *Client) Observe(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (result wire.Observation, err error) {
    finish := c.Trace.stage("observe")
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, "observe") }()
    if req.Version != wire.Version {
        return wire.Observation{}, &RemoteError{Code: "VERSION_MISMATCH"}
    }
    req.Action = "observe"
    data, err := json.Marshal(req)
    if err != nil {
        return wire.Observation{}, err
    }
    out, transportErr := c.executeRead(ctx, target, Quote(runner), data, batch)
    var response wire.Response
    if err := json.Unmarshal(out, &response); err != nil {
        if transportErr != nil {
            return wire.Observation{}, transportErr
        }
        return wire.Observation{}, errors.New("BAD_RESPONSE")
    }
    result = wire.Observation{Record: response.Record}
    if response.Log != nil {
        result.Log = *response.Log
    }
    if response.FormatVersion != wire.FormatVersion {
        return result, &RemoteError{Code: "BAD_FORMAT"}
    }
    if response.Version != wire.Version {
        return result, &RemoteError{Code: "VERSION_MISMATCH"}
    }
    if !response.OK {
        if response.Error == nil {
            return result, errors.New("BAD_RESPONSE")
        }
        return result, &RemoteError{response.Error.Code}
    }
    if transportErr != nil {
        return result, transportErr
    }
    if response.Record == nil || response.Log == nil {
        return result, errors.New("BAD_RESPONSE")
    }
    return result, nil
}

// Follow decodes one cancellable SSH stream. It never signals a remote run.
func (c *Client) Follow(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool, emit func(wire.LogEvent) error) (err error) {
    finish := c.Trace.stage("log_follow")
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, "log_follow") }()
    if req.Version != wire.Version {
        return &RemoteError{Code: "VERSION_MISMATCH"}
    }
    session, err := c.prepare(ctx, target, batch)
    if err != nil {
        return err
    }
    defer session.close()
    target, batch = session.target, session.batch
    req.Action = "follow"
    data, err := json.Marshal(req)
    if err != nil {
        return err
    }
    streamCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    cmd := commandContext(streamCtx, c.Binary, arguments(target, Quote(runner), batch)...)
    cmd.Env = session.env
    cmd.Stdin = bytes.NewReader(data)
    var diagnostic bytes.Buffer
    cmd.Stderr = &diagnostic
    output, err := cmd.StdoutPipe()
    if err != nil {
        return err
    }
    defer output.Close()
    if err := cmd.Start(); err != nil {
        return err
    }
    ended := false
    decoder := json.NewDecoder(output)
    var streamErr error
    decodeFailure := false
    for {
        var event struct {
            wire.LogEvent
            Error *wire.Error `json:"error"`
        }
        err := decoder.Decode(&event)
        if errors.Is(err, io.EOF) {
            break
        }
        if err != nil {
            streamErr = err
            decodeFailure = true
            break
        }
        if event.Record != nil && event.Record.Request.Version != wire.Version {
            streamErr = &RemoteError{Code: "VERSION_MISMATCH"}
            break
        }
        if event.Error != nil {
            streamErr = &RemoteError{event.Error.Code}
            break
        }
        if ended || event.Record == nil || (event.Type != "chunk" && event.Type != "end") {
            streamErr = errors.New("BAD_RESPONSE")
            break
        }
        if err := emit(event.LogEvent); err != nil {
            streamErr = err
            break
        }
        ended = event.Type == "end"
    }
    if streamErr != nil {
        cancel()
    }
    waitErr := cmd.Wait()
    if streamErr != nil {
        if decodeFailure {
            f := fault.Wrap("SSH_FAILED", "ssh_failed", session.transportError(streamErr))
            f.Params = map[string]any{"Detail": strings.TrimSpace(diagnostic.String())}
            return f
        }
        return streamErr
    }
    if waitErr != nil || !ended {
        f := fault.Wrap("SSH_FAILED", "ssh_failed", session.transportError(waitErr))
        f.Params = map[string]any{"Detail": strings.TrimSpace(diagnostic.String())}
        return f
    }
    return nil
}
