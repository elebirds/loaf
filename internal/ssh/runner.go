package ssh

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "regexp"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,100}$`)

// DeployRunner ensures the versioned executable matches the packaged runner.
// Replacing a mismatched file is atomic and never restarts a live supervisor.
func (c *Client) DeployRunner(ctx context.Context, target config.Target, version string, binary []byte, noninteractive bool) (result string, err error) {
    finish := c.Trace.stage("runner_verify")
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, "runner_verify") }()
    if !versionPattern.MatchString(version) || version != wire.Version || len(binary) == 0 {
        return "", errors.New("BAD_RUNNER")
    }
    probe := `target="$HOME/.local/share/loaf/bin/` + version + `/loaf-runner"; if [ -f "$target" ]; then printf '%s' "$target"; fi`
    out, err := c.executeRead(ctx, target, probe, nil, noninteractive)
    if err != nil {
        return "", fault.At(err, "runner_probe")
    }
    request := wire.Request{FormatVersion: wire.FormatVersion, Version: version, Action: "version"}
    remotePath := string(out)
    replace := remotePath != ""
    if replace {
        if !strings.HasPrefix(remotePath, "/") || strings.ContainsAny(remotePath, "\x00\r\n") {
            return "", errors.New("BAD_RUNNER_PATH")
        }
        err = c.verifyPackagedRunner(ctx, target, remotePath, request, noninteractive)
        if err == nil {
            return remotePath, nil
        }
        if err.Error() != "VERSION_MISMATCH" && err.Error() != "BAD_FORMAT" && err.Error() != "BAD_RESPONSE" {
            return "", err
        }
    }
    remotePath, err = c.uploadRunner(ctx, target, version, binary, noninteractive, replace)
    if err == nil {
        err = c.verifyPackagedRunner(ctx, target, remotePath, request, noninteractive)
    }
    return remotePath, err
}

// Compatible historical peers may serve control requests, but deployment must
// still verify the exact packaged identity at its versioned executable path.
func (c *Client) verifyPackagedRunner(ctx context.Context, target config.Target, runner string, request wire.Request, batch bool) error {
    response, err := c.ControlResponse(ctx, target, runner, request, batch)
    if err == nil && response.Version != request.Version {
        return &RemoteError{Code: "VERSION_MISMATCH"}
    }
    return err
}

func (c *Client) uploadRunner(ctx context.Context, target config.Target, version string, binary []byte, noninteractive, replace bool) (result string, err error) {
    finish := c.Trace.stage("runner_upload")
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, "runner_upload") }()
    publish := `if ! ln "$tmp" "$target"; then test -f "$target"; fi; rm "$tmp";`
    exists := `[ -e "$target" ]`
    if replace {
        exists = "false"
        publish = `mv -f "$tmp" "$target";`
    }
    command := `umask 077; set -eu; dir="$HOME/.local/share/loaf/bin/` + version + `"; mkdir -p "$dir"; target="$dir/loaf-runner"; if ` + exists + `; then cat >/dev/null; else tmp=$(mktemp "$dir/.upload.XXXXXX"); trap 'rm -f "$tmp"' EXIT HUP INT TERM; cat >"$tmp"; test "$(wc -c <"$tmp")" -eq ` + fmt.Sprint(len(binary)) + `; chmod 700 "$tmp"; ` + publish + ` trap - EXIT HUP INT TERM; fi; printf '%s' "$target"`
    out, err := c.Execute(ctx, target, command, bytes.NewReader(binary), noninteractive)
    if err != nil {
        return "", err
    }
    remotePath := string(out)
    if !strings.HasPrefix(remotePath, "/") || strings.ContainsAny(remotePath, "\x00\r\n") {
        return "", errors.New("BAD_RUNNER_PATH")
    }
    return remotePath, nil
}

// Control transports one JSON request without interpolating its fields into Shell.
func (c *Client) Control(ctx context.Context, target config.Target, runner string, request wire.Request, noninteractive bool) (record *wire.RunRecord, err error) {
    response, err := c.ControlResponse(ctx, target, runner, request, noninteractive)
    return response.Record, err
}

// ControlResponse retains all structured partial results from a finite request.
// New queue/event callers share the original version checks and mutation safety.
func (c *Client) ControlResponse(ctx context.Context, target config.Target, runner string, request wire.Request, noninteractive bool) (response wire.Response, err error) {
    stage := request.Action
    if stage == "version" {
        stage = "runner_verify"
    }
    finish := c.Trace.stage(stage)
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, stage) }()
    if !wire.CompatibleVersion(wire.Version, request.Version) {
        return response, &RemoteError{Code: "VERSION_MISMATCH"}
    }
    data, err := json.Marshal(request)
    if err != nil {
        return response, err
    }
    var out []byte
    var sshErr error
    if readOnlyControl(request) {
        out, sshErr = c.executeRead(ctx, target, Quote(runner), data, noninteractive)
    } else {
        out, sshErr = c.Execute(ctx, target, Quote(runner), bytes.NewReader(data), noninteractive)
    }
    if err := json.Unmarshal(out, &response); err != nil {
        if sshErr != nil {
            return wire.Response{}, sshErr
        }
        return wire.Response{}, errors.New("BAD_RESPONSE")
    }
    if response.FormatVersion != wire.FormatVersion {
        return wire.Response{}, &RemoteError{Code: "BAD_FORMAT"}
    }
    if !wire.CompatibleVersion(wire.Version, response.Version) {
        return wire.Response{}, &RemoteError{Code: "VERSION_MISMATCH"}
    }
    if !response.OK {
        if response.Error == nil {
            return response, errors.New("BAD_RESPONSE")
        }
        return response, &RemoteError{response.Error.Code}
    }
    if sshErr != nil {
        return response, sshErr
    }
    return response, nil
}

func readOnlyControl(request wire.Request) bool {
    switch request.Action {
    case "version", "status", "logs", "events":
        return true
    case "queue":
        if request.Queue != nil {
            switch request.Queue.Action {
            case "list", "show", "status", "events", "clean-preview":
                return true
            }
        }
    }
    return false
}
