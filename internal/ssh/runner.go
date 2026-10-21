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
    "github.com/elebirds/loaf/internal/wire"
)

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,100}$`)

// DeployRunner ensures the versioned executable matches the packaged runner.
// Replacing a mismatched file is atomic and never restarts a live supervisor.
func (c *Client) DeployRunner(ctx context.Context, target config.Target, version string, binary []byte, noninteractive bool) (string, error) {
    if !versionPattern.MatchString(version) || version != wire.Version || len(binary) == 0 {
        return "", errors.New("BAD_RUNNER")
    }
    probe := `target="$HOME/.local/share/loaf/bin/` + version + `/loaf-runner"; if [ -f "$target" ]; then printf '%s' "$target"; fi`
    out, err := c.Execute(ctx, target, probe, nil, noninteractive)
    if err != nil {
        return "", err
    }
    request := wire.Request{FormatVersion: wire.FormatVersion, Version: version, Action: "version"}
    remotePath := string(out)
    replace := remotePath != ""
    if replace {
        if !strings.HasPrefix(remotePath, "/") || strings.ContainsAny(remotePath, "\x00\r\n") {
            return "", errors.New("BAD_RUNNER_PATH")
        }
        _, err = c.Control(ctx, target, remotePath, request, noninteractive)
        if err == nil {
            return remotePath, nil
        }
        if err.Error() != "VERSION_MISMATCH" && err.Error() != "BAD_FORMAT" && err.Error() != "BAD_RESPONSE" {
            return "", err
        }
    }
    remotePath, err = c.uploadRunner(ctx, target, version, binary, noninteractive, replace)
    if err == nil {
        _, err = c.Control(ctx, target, remotePath, request, noninteractive)
    }
    return remotePath, err
}

func (c *Client) uploadRunner(ctx context.Context, target config.Target, version string, binary []byte, noninteractive, replace bool) (string, error) {
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
func (c *Client) Control(ctx context.Context, target config.Target, runner string, request wire.Request, noninteractive bool) (*wire.RunRecord, error) {
    if request.Version != wire.Version {
        return nil, &RemoteError{Code: "VERSION_MISMATCH"}
    }
    data, err := json.Marshal(request)
    if err != nil {
        return nil, err
    }
    out, sshErr := c.Execute(ctx, target, Quote(runner), bytes.NewReader(data), noninteractive)
    var response wire.Response
    if err := json.Unmarshal(out, &response); err != nil {
        if sshErr != nil {
            return nil, sshErr
        }
        return nil, errors.New("BAD_RESPONSE")
    }
    if response.FormatVersion != wire.FormatVersion {
        return nil, &RemoteError{Code: "BAD_FORMAT"}
    }
    if response.Version != wire.Version {
        return nil, &RemoteError{Code: "VERSION_MISMATCH"}
    }
    if !response.OK {
        if response.Error == nil {
            return response.Record, errors.New("BAD_RESPONSE")
        }
        return response.Record, &RemoteError{response.Error.Code}
    }
    if sshErr != nil {
        return response.Record, sshErr
    }
    return response.Record, nil
}
