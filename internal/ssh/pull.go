package ssh

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "regexp"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// RunnerArch observes only the platform needed to deploy a read-only helper.
// Downloading existing results must not require a writable experiment directory.
func (c *Client) RunnerArch(ctx context.Context, target config.Target, batch bool) (arch string, err error) {
    finish := c.Trace.stage("platform_probe")
    defer func() { finish(err) }()
    out, err := c.executeRead(ctx, target, "uname -s; uname -m", nil, batch)
    if err != nil {
        return "", fault.At(err, "platform_probe")
    }
    fields := strings.Fields(string(out))
    if len(fields) != 2 || fields[0] != "Linux" || (fields[1] != "x86_64" && fields[1] != "aarch64") {
        return "", fault.New("CHECK_FAILED", "pull_platform", nil)
    }
    return fields[1], nil
}

// Outputs reads a transient selection using the matching current helper.
// It never requests execution.
func (c *Client) Outputs(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (result wire.OutputSelection, err error) {
    finish := c.Trace.stage("outputs")
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, "outputs") }()
    req.Action = "outputs"
    data, err := json.Marshal(req)
    if err != nil {
        return wire.OutputSelection{}, err
    }
    out, transportErr := c.executeRead(ctx, target, Quote(runner), data, batch)
    var response wire.Response
    if err := json.Unmarshal(out, &response); err != nil {
        if transportErr != nil {
            return wire.OutputSelection{}, transportErr
        }
        return wire.OutputSelection{}, pullFailure("BAD_RESPONSE", err, "")
    }
    if response.FormatVersion != wire.FormatVersion {
        return wire.OutputSelection{}, pullFailure("BAD_FORMAT", nil, "")
    }
    if response.Version != wire.Version || req.Version != wire.Version {
        return wire.OutputSelection{}, pullFailure("VERSION_MISMATCH", nil, "")
    }
    if !response.OK {
        if response.Error == nil {
            return wire.OutputSelection{}, pullFailure("BAD_RESPONSE", transportErr, "")
        }
        detail := ""
        if response.Error.Path != "" {
            detail = fmt.Sprintf("Path: %q", response.Error.Path)
        }
        return wire.OutputSelection{}, pullFailure(response.Error.Code, transportErr, detail)
    }
    if transportErr != nil {
        return wire.OutputSelection{}, transportErr
    }
    if response.Outputs == nil {
        return wire.OutputSelection{}, pullFailure("BAD_RESPONSE", nil, "")
    }
    return *response.Outputs, nil
}

// PullInput contains only one invocation's saved location and literal subpaths.
type PullInput struct {
    Root, ProjectID, RunID string
    Paths                  []string
    Checksum               bool
    DestinationFS          *wire.OutputFilesystem
}

// Pull updates a checked local destination without mirror deletion or in-place
// writes. files-from uses rsync's native protocol and treats names literally.
func (r Rsync) Pull(ctx context.Context, target config.Target, runner, destination string, input PullInput, batch bool) (result Transfer, err error) {
    attempts := 0
    var trace *Trace
    if r.Client != nil {
        trace = r.Client.Trace
    }
    finish := trace.stage("pull")
    defer func() { finish(err) }()
    err = retryReadTrace(ctx, trace, func() (err error) {
        attempts++
        result, err = r.pull(ctx, target, runner, destination, input, batch)
        return err
    })
    result.Retried = attempts > 1
    return result, fault.At(err, "pull")
}

func (r Rsync) pull(ctx context.Context, target config.Target, runner, destination string, input PullInput, batch bool) (Transfer, error) {
    var result Transfer
    if r.Client == nil || r.Client.Binary == "" {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    if r.Binary == "" {
        return result, fault.New("PULL_FAILED", "pull_rsync_missing", nil)
    }
    session, err := r.Client.prepare(ctx, target, batch)
    if err != nil {
        return result, err
    }
    defer session.close()
    target, batch = session.target, session.batch
    if len(input.Paths) == 0 {
        return result, pullFailure("BAD_REQUEST", nil, "")
    }
    for _, name := range input.Paths {
        if !wire.ValidOutputPath(name) {
            return result, pullFailure("UNSAFE_PATH", nil, fmt.Sprintf("Path: %q", name))
        }
    }
    paths, err := json.Marshal(input.Paths)
    if err != nil {
        return result, err
    }
    words := []string{runner, "__send", wire.Version, input.Root, input.ProjectID, input.RunID, string(paths)}
    if input.DestinationFS != nil {
        descriptor, err := json.Marshal(input.DestinationFS)
        if err != nil {
            return result, err
        }
        words = append(words, "--destination-filesystem="+string(descriptor))
    }
    sender, err := r.receiverCommand(ctx, words)
    if err != nil {
        return result, pullFailure("PULL_FAILED", err, "")
    }
    shell, host := r.remoteShell(target, batch)
    args := []string{"-rlt", "--relative", "--safe-links", "--from0", "--files-from=-", "--out-format=LOAF_CHANGE:%i", "--rsync-path=" + sender, "-e", shell}
    if input.Checksum {
        args = append(args, "--checksum")
    }
    local, err := r.Client.localTransferPath(ctx, destination)
    if err != nil {
        return result, err
    }
    args = append(args, "--", host+":.", local+"/")
    cmd := commandContext(ctx, r.Binary, args...)
    cmd.Env = session.env
    cmd.Stdin = strings.NewReader(strings.Join(input.Paths, "\x00") + "\x00")
    var changes changeCounter
    diagnostic := &diagnosticTail{}
    cmd.Stdout, cmd.Stderr = &changes, diagnostic
    cancelTransfer(cmd, true)
    err = cmd.Run()
    if ctx.Err() != nil {
        return result, pullFailure("PULL_CANCELED", ctx.Err(), diagnostic.String())
    }
    if err != nil {
        code := "PULL_FAILED"
        if match := pullCode.FindStringSubmatch(diagnostic.String()); match != nil {
            code = match[1]
        }
        return result, pullFailure(code, session.transportError(err), diagnostic.String())
    }
    result.ChangedFiles = changes.count
    return result, nil
}

var pullCode = regexp.MustCompile(`(?m)^LOAF_PULL_ERROR:([A-Z_]+)$`)

func pullFailure(code string, cause error, diagnostic string) *fault.Error {
    message, hint := "pull_failed", "pull_retry"
    switch code {
    case "UNSAFE_PATH", "PROJECT_MISMATCH":
        message, hint = "pull_unsafe", "pull_path_hint"
    case "BAD_FORMAT", "BAD_SENDER", "VERSION_MISMATCH", "BAD_RESPONSE", "BAD_REQUEST":
        message, hint = "pull_format", "pull_format_hint"
    case "RUN_NOT_FOUND":
        message = "run_not_found"
    case "OUTPUT_MISSING":
        message = "pull_missing"
    case "OUTPUT_READ_FAILED":
        message = "pull_read"
    case "PULL_CANCELED":
        message = "pull_canceled"
    case "RUN_CLEANED":
        message, hint = "clean_run_cleaned", ""
    case "CLEAN_UNCONFIRMED", "CLEAN_BUSY":
        message, hint = "clean_unconfirmed", "clean_retry"
    }
    f := fault.Wrap(code, message, cause)
    f.Hint = hint
    f.Params = map[string]any{"Detail": strings.TrimSpace(diagnostic)}
    return f
}

// Count itemized content changes without retaining a line per output file.
type changeCounter struct {
    pending string
    count   int
}

func (c *changeCounter) Write(data []byte) (int, error) {
    size := len(data)
    for len(data) > 0 {
        line, rest, found := bytes.Cut(data, []byte{'\n'})
        if len(c.pending) < 256 {
            c.pending += string(line[:min(len(line), 256-len(c.pending))])
        }
        if !found {
            break
        }
        item, ok := strings.CutPrefix(c.pending, "LOAF_CHANGE:")
        if ok && len(item) >= 2 && strings.ContainsRune("<>c", rune(item[0])) && (item[1] == 'f' || item[1] == 'L') {
            c.count++
        }
        c.pending = ""
        data = rest
    }
    return size, nil
}

type diagnosticTail struct{ data []byte }

func (d *diagnosticTail) Write(data []byte) (int, error) {
    size := len(data)
    const limit = 64 * 1024
    if len(data) >= limit {
        d.data = append(d.data[:0], data[len(data)-limit:]...)
    } else {
        if len(d.data)+len(data) > limit {
            d.data = d.data[len(d.data)+len(data)-limit:]
        }
        d.data = append(d.data, data...)
    }
    return size, nil
}

func (d *diagnosticTail) String() string { return string(d.data) }
