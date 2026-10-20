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

// DeployRunner uploads an embedded runner into an immutable version directory.
func (c *Client) DeployRunner(ctx context.Context, target config.Target, version string, binary []byte, noninteractive bool) (string, error) {
    if !versionPattern.MatchString(version) || len(binary) == 0 {
        return "", errors.New("BAD_RUNNER")
    }
    command := `umask 077; set -eu; dir="$HOME/.local/share/loaf/bin/` + version + `"; mkdir -p "$dir"; target="$dir/loaf-runner"; if [ -e "$target" ]; then cat >/dev/null; else tmp=$(mktemp "$dir/.upload.XXXXXX"); trap 'rm -f "$tmp"' EXIT HUP INT TERM; cat >"$tmp"; test "$(wc -c <"$tmp")" -eq ` + fmt.Sprint(len(binary)) + `; chmod 700 "$tmp"; if ! ln "$tmp" "$target"; then test -f "$target"; fi; rm "$tmp"; trap - EXIT HUP INT TERM; fi; printf '%s' "$target"`
    out, err := c.Execute(ctx, target, command, bytes.NewReader(binary), noninteractive)
    if err != nil {
        return "", err
    }
    remotePath := string(out)
    if !strings.HasPrefix(remotePath, "/") || strings.ContainsAny(remotePath, "\x00\r\n") {
        return "", errors.New("BAD_RUNNER_PATH")
    }
    _, err = c.Control(ctx, target, remotePath, wire.Request{FormatVersion: 1, Version: version, Action: "version"}, noninteractive)
    return remotePath, err
}

// Control transports one JSON request without interpolating its fields into Shell.
func (c *Client) Control(ctx context.Context, target config.Target, runner string, request wire.Request, noninteractive bool) (*wire.RunRecord, error) {
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
    if response.FormatVersion != 1 || response.Version != request.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    if !response.OK {
        if response.Error == nil {
            return response.Record, errors.New("BAD_RESPONSE")
        }
        return response.Record, errors.New(response.Error.Code)
    }
    if sshErr != nil {
        return response.Record, sshErr
    }
    return response.Record, nil
}
