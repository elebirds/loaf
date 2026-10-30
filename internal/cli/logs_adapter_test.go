package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/wire"
)

type uxERemote struct {
    runRemote
    requests     []wire.Request
    targets      []config.Target
    mode         string
    controls     int
    beforeSecond func()
    entered      chan struct{}
    release      chan struct{}
}

func (r *uxERemote) Control(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (*wire.RunRecord, error) {
    r.controls++
    return r.runRemote.Control(ctx, target, runner, req, batch)
}

func uxECursor(req wire.Request, offset int64) string {
    token, _ := wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: req.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, FileID: "1:2", Offset: offset, Boundary: strings.Repeat("a", 64)})
    return token
}

func (r *uxERemote) Observe(_ context.Context, target config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    r.requests = append(r.requests, req)
    r.targets = append(r.targets, target)
    text := "中文\x00\xff\x1b[31m private-log\n"
    return wire.Observation{Record: r.record(req), Log: wire.LogChunk{Text: text, Offset: int64(len(text)), Cursor: uxECursor(req, int64(len(text)))}}, nil
}

func (r *uxERemote) Follow(ctx context.Context, target config.Target, _ string, req wire.Request, _ bool, emit func(wire.LogEvent) error) error {
    r.requests = append(r.requests, req)
    r.targets = append(r.targets, target)
    call := len(r.requests)
    if r.entered != nil {
        close(r.entered)
        select {
        case <-r.release:
        case <-ctx.Done():
            return ctx.Err()
        }
    }
    if r.mode == "cancel" {
        return fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", context.Canceled)
    }
    disconnected := fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Connection reset by peer"})
    if r.mode == "changed" && call == 1 {
        return &ssh.RemoteError{Code: "LOG_CHANGED"}
    }
    if r.mode == "exhausted" && call > 1 || r.mode == "twice" && call == 2 {
        return disconnected
    }
    record := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123}
    text, offset := "first 中文\n", int64(len("first 中文\n"))
    if req.Resume != "" {
        cursor, _ := wire.DecodeLogCursor(req.Resume)
        text, offset = "second\n", cursor.Offset+int64(len("second\n"))
    }
    if r.mode == "bytes" {
        text = "中文\x00\xff\x1b[31m private-log\n"
        offset = int64(len(text))
    }
    if err := emit(wire.LogEvent{Type: "chunk", Record: record, Log: wire.LogChunk{Text: text, Offset: offset, Cursor: uxECursor(req, offset)}}); err != nil {
        return err
    }
    if ((r.mode == "disconnect" || r.mode == "twice") && call == 1) || r.mode == "exhausted" {
        return disconnected
    }
    if r.mode == "eof" {
        return nil
    }
    if r.beforeSecond != nil {
        r.beforeSecond()
    }
    exit := 7
    record.Status, record.ExitCode = "failed", &exit
    if err := emit(wire.LogEvent{Type: "end", Record: record, Log: wire.LogChunk{Cursor: uxECursor(req, offset), Offset: offset}}); err != nil {
        return err
    }
    if r.mode == "end-disconnect" {
        return disconnected
    }
    return nil
}

func uxEFixture(t *testing.T, input, language string) (*invocation, *uxERemote, testOptions, string) {
    t.Helper()
    o, remote := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "true"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    id := remote.req.RunID
    read := &uxERemote{}
    o.Runs.Remote, o.Runs.Observations = read, o.State
    o.Interactive, o.TerminalOutput = true, true
    o.In = strings.NewReader(input)
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l, err := i18n.New(language, nil)
    if err != nil {
        t.Fatal(err)
    }
    c := &invocation{ui: true, app: client.New(o.Dependencies, settings), options: o.Options, grammar: &grammar{}, output: &presenter{locale: l, out: o.Out, err: o.Err, command: "logs"}, input: interaction.New(interaction.Options{In: o.In, Err: o.Err, Interactive: true})}
    return c, read, o, id
}

func TestUXEDirectJSONAndPipeRetainAcknowledgementAndOffsets(t *testing.T) {
    var frames []map[string]any
    for _, language := range []string{"en", "zh-Hans"} {
        for _, mode := range []string{"json", "pipe", "terminal"} {
            c, remote, o, id := uxEFixture(t, "", language)
            remote.mode = "bytes"
            checkpoint := filepath.Join(t.TempDir(), "cursor.json")
            c.grammar.Logs.ID, c.grammar.Logs.Follow, c.grammar.Logs.ResumeFile = id, true, &checkpoint
            c.output.json = mode == "json"
            c.options.TerminalOutput = mode == "terminal"
            c.input = interaction.New(interaction.Options{In: uxCoreNoInput{t}})
            data, err := c.directLogs(context.Background())
            if err != nil || data != nil {
                t.Fatal(data, err)
            }
            token, err := local.ReadLogCheckpoint(checkpoint)
            if err != nil || (token != "") != (mode == "terminal") {
                t.Fatal(mode, token, err)
            }
            if mode != "json" {
                if o.Out.(*bytes.Buffer).String() != "中文\x00\xff\x1b[31m private-log\n" {
                    t.Fatal(o.Out)
                }
                continue
            }
            if o.Err.(*bytes.Buffer).Len() != 0 {
                t.Fatal(o.Err)
            }
            rows := strings.Split(strings.TrimSpace(o.Out.(*bytes.Buffer).String()), "\n")
            if len(rows) != 2 {
                t.Fatal(rows)
            }
            for n, row := range rows {
                var frame map[string]any
                if json.Unmarshal([]byte(row), &frame) != nil {
                    t.Fatal(row)
                }
                data := frame["data"].(map[string]any)
                run := data["run"].(map[string]any)
                log := run["log"].(map[string]any)
                if log["offset"] != float64(len("中文\x00\xff\x1b[31m private-log\n")) || run["target"] != "lab" {
                    t.Fatal(frame)
                }
                // Normalize only fixture identities and timestamps, not keys,
                // event kinds, text encoding, offsets or terminal status.
                for _, key := range []string{"run_id", "remote_dir", "created_at", "observed_at"} {
                    delete(run, key)
                }
                delete(log, "cursor")
                if language == "en" {
                    frames = append(frames, frame)
                } else if !reflect.DeepEqual(frames[n], frame) {
                    t.Fatal(frames[n], frame)
                }
            }
        }
    }
}

func TestUXEActionsRejectUnsafeCursorAndFailures(t *testing.T) {
    c, _, _, id := uxEFixture(t, "", "en")
    details, err := c.app.Projects.Inspect(context.Background(), id)
    if err != nil {
        t.Fatal(err)
    }
    token := uxECursor(wire.Request{Version: wire.Version, Root: details.RemoteDir, ProjectID: details.ProjectID, RunID: id}, 4)
    result := client.LogsResult{View: execution.View{ID: id, Version: wire.Version, RemoteDir: details.RemoteDir, Ref: details.Ref, Log: &wire.LogChunk{Offset: 4, Cursor: token}}}
    cause := fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Connection reset by peer"})
    actions := logRecoveryActions(result, logReceipt{}, cause, true)
    if len(actions) != 1 || !reflect.DeepEqual(actions[0].Command, []string{"loaf", "logs", id, "-f", "--resume", token}) || !actions[0].RequiresConfirmation {
        t.Fatal(actions)
    }
    for _, blocked := range []error{io.ErrClosedPipe, context.Canceled, fault.New("LOG_CONSUMER_FAILED", "log_consumer_failed", nil), fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Permission denied"})} {
        if actions := logRecoveryActions(result, logReceipt{}, blocked, true); len(actions) != 0 {
            t.Fatal(actions, blocked)
        }
    }
    if len(logRecoveryActions(result, logReceipt{}, cause, false)) != 0 {
        t.Fatal("pipe was acknowledged")
    }
    result.View.Log.Cursor = strings.Repeat("a", 8193)
    if len(logRecoveryActions(result, logReceipt{}, cause, true)) != 0 {
        t.Fatal("invalid cursor offered")
    }
    for _, req := range []wire.Request{
        {Version: wire.Version, Root: details.RemoteDir, ProjectID: "another-project", RunID: id},
        {Version: wire.Version, Root: "/another/root", ProjectID: details.ProjectID, RunID: id},
    } {
        result.View.Log.Cursor = uxECursor(req, 4)
        if len(logRecoveryActions(result, logReceipt{}, cause, true)) != 0 {
            t.Fatal("cursor changed binding", req)
        }
    }
}

func TestUXENativeOwnershipReleasedAfterRead(t *testing.T) {
    c, _, _, id := uxEFixture(t, "after\n", "en")
    _, _, err := c.readLogs(context.Background(), client.LogsInput{ID: id, Follow: true})
    if err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), time.Second)
    defer cancel()
    value, err := c.input.Plain().Text(ctx, interaction.TextPrompt{Title: "next"})
    if err != nil || value != "after" {
        t.Fatal(value, err)
    }
}

func TestUXEDirectFullInvocationNeverPrompts(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, mode := range []string{"tty", "NO_COLOR", "dumb", "pipe", "json"} {
            _, remote, o, id := uxEFixture(t, "", language)
            o.In = uxCoreNoInput{t}
            o.TerminalOutput = mode != "pipe"
            o.Env["NO_COLOR"], o.Env["TERM"] = "", "xterm-256color"
            if mode == "NO_COLOR" {
                o.Env["NO_COLOR"] = "1"
            }
            if mode == "dumb" {
                o.Env["TERM"] = "dumb"
            }
            args := []string{"--lang", language, "logs", "-f"}
            if mode != "pipe" {
                args = append(args, id)
            }
            if mode == "json" {
                args = append(args, "--json")
            }
            code, out, stderr := run(t, o, args...)
            if code != 0 || len(remote.requests) != 1 || remote.requests[0].RunID != id || remote.requests[0].Tail != 100 || remote.controls != 0 {
                t.Fatal(code, out, stderr, remote.requests)
            }
            if mode == "json" {
                if stderr != "" || !strings.Contains(out, `"type":"end"`) {
                    t.Fatal(out, stderr)
                }
            } else if out != "first 中文\n" || !strings.Contains(stderr, id) || strings.Contains(stderr, "\x1b[") {
                t.Fatal(out, stderr)
            }
        }
    }
}

type logFailureWriter struct{}

func (logFailureWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDirectLogCancellationAndConsumerFailureNeverReopenInput(t *testing.T) {
    for _, mode := range []string{"cancel", "consumer"} {
        c, remote, o, id := uxEFixture(t, "", "en")
        c.grammar.Logs.ID, c.grammar.Logs.Follow = id, true
        c.input = interaction.New(interaction.Options{In: uxCoreNoInput{t}})
        if mode == "cancel" {
            remote.mode = "cancel"
        } else {
            c.output.out = logFailureWriter{}
        }
        _, err := c.directLogs(context.Background())
        var failure *fault.Error
        want := "OBSERVATION_CANCELED"
        if mode == "consumer" {
            want = "LOG_CONSUMER_FAILED"
        }
        if !errors.As(err, &failure) || failure.Code != want || len(remote.requests) != 1 || remote.controls != 0 || strings.Contains(o.Err.(*bytes.Buffer).String(), "loaf logs") {
            t.Fatal(mode, err, remote.requests, remote.controls, o.Err)
        }
    }
}

func TestDirectLogRecoveryOnlyPrintsAnExplicitContinuation(t *testing.T) {
    c, remote, o, id := uxEFixture(t, "", "en")
    remote.mode = "twice"
    checkpoint := filepath.Join(t.TempDir(), "cursor.json")
    c.grammar.Logs.ID, c.grammar.Logs.Follow, c.grammar.Logs.ResumeFile = id, true, &checkpoint
    c.input = interaction.New(interaction.Options{In: uxCoreNoInput{t}})
    _, err := c.directLogs(context.Background())
    text := o.Err.(*bytes.Buffer).String()
    if err == nil || len(remote.requests) != 2 || remote.requests[0].Resume != "" || remote.requests[1].Resume == "" || !strings.Contains(text, "--resume ") || strings.Contains(text, "--resume-file") {
        t.Fatal(err, remote.requests, text)
    }
    if token, err := local.ReadLogCheckpoint(checkpoint); err != nil || token == "" {
        t.Fatal(token, err)
    }
}
