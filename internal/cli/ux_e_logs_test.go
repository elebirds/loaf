package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
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

func TestUXEMenuUsesFrozenIdentityAndUnmodifiedLogBytes(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        c, remote, o, id := uxEFixture(t, "1\n\n", language)
        if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[logs]\ntail = 17\n"), 0600); err != nil {
            t.Fatal(err)
        }
        for _, args := range [][]string{{"machine", "add", "other", "--ssh", "other", "--json"}, {"machine", "use", "other", "--json"}} {
            if code, out, stderr := run(t, o, args...); code != 0 {
                t.Fatal(code, out, stderr)
            }
        }
        data, err := c.uxELogs(context.Background(), id)
        out, stderr := o.Out.(*bytes.Buffer).String(), o.Err.(*bytes.Buffer).String()
        if err != nil || data != nil || len(remote.requests) != 1 || remote.requests[0].RunID != id || remote.requests[0].Tail != 17 || remote.targets[0].Destination != "lab" || remote.controls != 0 {
            t.Fatal(data, err, remote.requests, remote.targets, remote.controls)
        }
        if out != "中文\x00\xff\x1b[31m private-log\n" || strings.Contains(stderr, "private-log") || strings.Contains(stderr, "other") || strings.Count(stderr, c.output.locale.Text("ux_e_tail_done")) != 1 {
            t.Fatal(out, stderr)
        }
    }
}

func TestUXEMenuRecoveryRequiresExplicitChoiceAndDoesNotRepeatTail(t *testing.T) {
    for _, choice := range []string{"1", "2"} {
        c, remote, o, id := uxEFixture(t, "2\n\n"+choice+"\n", "en")
        remote.mode = "disconnect"
        if data, err := c.uxELogs(context.Background(), id); data != nil || err != nil {
            t.Fatal(data, err)
        }
        want, calls := "first 中文\n", 1
        if choice == "2" {
            want, calls = want+"second\n", 2
            first := remote.requests[0]
            cursor, err := wire.DecodeLogCursor(remote.requests[1].Resume)
            if err != nil || cursor.Offset != int64(len("first 中文\n")) || !cursor.Matches(first) || remote.requests[1].RunID != id {
                t.Fatal(cursor, err, remote.requests)
            }
        }
        if len(remote.requests) != calls || o.Out.(*bytes.Buffer).String() != want || remote.controls != 0 {
            t.Fatal(remote.requests, o.Out, remote.controls)
        }
        if strings.Contains(o.Err.(*bytes.Buffer).String(), "--resume") != (choice == "1") {
            t.Fatal("recovery menu exposed an unnecessary cursor, or exit lost it", o.Err)
        }
    }
}

func TestUXEFileRecoveryMakesCursorOnlyContinuationExplicit(t *testing.T) {
    checkpoint := filepath.Join(t.TempDir(), "cursor.json")
    c, remote, o, id := uxEFixture(t, "3\n3\n"+checkpoint+"\n1\n2\n", "en")
    remote.mode = "twice"
    if _, err := c.uxELogs(context.Background(), id); err != nil {
        t.Fatal(err)
    }
    if len(remote.requests) != 3 || remote.requests[2].Resume == "" || o.Out.(*bytes.Buffer).String() != "first 中文\nsecond\n" || !strings.Contains(o.Err.(*bytes.Buffer).String(), c.output.locale.Text("ux_e_cursor_only")) {
        t.Fatal(remote.requests, o.Out, o.Err)
    }
    token, err := local.ReadLogCheckpoint(checkpoint)
    cursor, decodeErr := wire.DecodeLogCursor(token)
    if err != nil || decodeErr != nil || cursor.Offset != int64(len("first 中文\n")) {
        t.Fatal(cursor, err, decodeErr)
    }
}

func TestUXELogChangedRequiresExplicitFreshRead(t *testing.T) {
    for _, choice := range []string{"1", "2"} {
        c, remote, o, id := uxEFixture(t, "2\n\n"+choice+"\n0\n", "zh-Hans")
        remote.mode = "changed"
        if _, err := c.uxELogs(context.Background(), id); err != nil {
            t.Fatal(err)
        }
        want := 1
        if choice == "2" {
            want = 2
            if req := remote.requests[1]; req.Resume != "" || req.Tail != 0 || req.RunID != id {
                t.Fatal(req)
            }
        }
        if len(remote.requests) != want || remote.controls != 0 || strings.Contains(o.Err.(*bytes.Buffer).String(), "--resume ") {
            t.Fatal(remote.requests, o.Err)
        }
    }
}

func TestUXEAdvancedOptionsKeepExplicitZeroAndCursorBounds(t *testing.T) {
    c, remote, _, id := uxEFixture(t, "3\n1\n0\n3\n0s\n", "en")
    if _, err := c.uxELogs(context.Background(), id); err != nil || len(remote.requests) != 1 || remote.requests[0].Tail != 0 {
        t.Fatal(err, remote.requests)
    }
    c, remote, _, id = uxEFixture(t, "", "en")
    details, err := c.app.Projects.Inspect(context.Background(), id)
    if err != nil {
        t.Fatal(err)
    }
    token := uxECursor(wire.Request{Version: wire.Version, Root: details.RemoteDir, ProjectID: details.ProjectID, RunID: id}, 4)
    c.input = interaction.New(interaction.Options{Interactive: true, In: strings.NewReader("3\n2\ninvalid\n" + token + "\n1\n"), Err: c.output.err})
    if _, err := c.uxELogs(context.Background(), id); err != nil || len(remote.requests) != 1 || remote.requests[0].Resume != token {
        t.Fatal(err, remote.requests)
    }
}

func TestUXEBackCancellationAndBatchNeverObserve(t *testing.T) {
    c, remote, _, id := uxEFixture(t, "4\n", "en")
    if _, err := c.uxELogs(context.Background(), id); err != nil || len(remote.requests) != 0 {
        t.Fatal(err, remote.requests)
    }
    for _, mode := range []string{"empty-id", "batch", "json", "noninteractive", "canceled"} {
        c, remote, _, id := uxEFixture(t, "", "en")
        c.input = interaction.New(interaction.Options{In: uxCoreNoInput{t}, Interactive: true})
        ctx, cancel := context.WithCancel(context.Background())
        switch mode {
        case "empty-id":
            id = ""
        case "batch":
            c.policy.Batch = true
        case "json":
            c.output.json = true
        case "noninteractive":
            c.options.Interactive = false
        case "canceled":
            cancel()
        }
        _, err := c.uxELogs(ctx, id)
        cancel()
        if err == nil || len(remote.requests) != 0 {
            t.Fatal(mode, err, remote.requests)
        }
    }
}

type uxEFailWriter struct {
    calls int
    cause error
}

func (w *uxEFailWriter) Write(p []byte) (int, error) {
    w.calls++
    if w.calls > 1 {
        return 0, w.cause
    }
    return len(p), nil
}

func TestUXEConsumptionCancellationAndCheckpointFailuresDoNotReopenInput(t *testing.T) {
    for _, mode := range []string{"consumer", "consumer-fault", "checkpoint", "cancel"} {
        t.Run(mode, func(t *testing.T) {
            c, remote, _, id := uxEFixture(t, "", "en")
            c.input = interaction.New(interaction.Options{In: uxCoreNoInput{t}})
            in := client.LogsInput{ID: id, Follow: true, Reconnect: true}
            checkpoint := filepath.Join(t.TempDir(), "cursor.json")
            if mode == "checkpoint" {
                in.ResumeFile = &checkpoint
                remote.beforeSecond = func() {
                    if err := os.Remove(checkpoint + ".lock"); err != nil {
                        t.Fatal(err)
                    }
                    if err := os.Mkdir(checkpoint+".lock", 0700); err != nil {
                        t.Fatal(err)
                    }
                }
            } else if mode == "cancel" {
                remote.mode = "cancel"
            } else {
                cause := error(io.ErrClosedPipe)
                if mode == "consumer-fault" {
                    cause = fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Connection reset by peer"})
                }
                c.output.out = &uxEFailWriter{cause: cause}
            }
            result, receipt, err := c.uxEReadLogs(context.Background(), in)
            if err == nil || len(remote.requests) != 1 || remote.controls != 0 || len(uxELogActions(result, receipt, err, true)) != 0 {
                t.Fatal(result, receipt, err, remote.requests)
            }
            if mode == "checkpoint" {
                token, readErr := local.ReadLogCheckpoint(checkpoint)
                cursor, decodeErr := wire.DecodeLogCursor(token)
                if readErr != nil || decodeErr != nil || cursor.Offset != int64(len("first 中文\n")) || result.View.StateError != "LOG_CHECKPOINT_FAILED" || result.View.QueryError != "" {
                    t.Fatal(result, readErr, decodeErr, cursor)
                }
            }
            if mode == "cancel" && !uxELogCanceled(context.Background(), err) {
                t.Fatal(err)
            }
        })
    }
}

func TestUXEEOFEndAndExhaustionHaveDistinctEvidence(t *testing.T) {
    for _, mode := range []string{"eof", "end-disconnect", "exhausted", "success"} {
        c, remote, o, id := uxEFixture(t, "", "en")
        remote.mode = mode
        budget := 25 * time.Millisecond
        in := client.LogsInput{ID: id, Follow: true}
        if mode == "exhausted" {
            in.RecoveryBudget = &budget
        }
        result, receipt, err := c.uxEReadLogs(context.Background(), in)
        if mode == "eof" || mode == "success" {
            if err != nil {
                t.Fatal(err)
            }
            if err := c.uxELogFinished(result, receipt, true); err != nil {
                t.Fatal(err)
            }
            key := "ux_e_follow_done"
            if mode == "eof" {
                key = "ux_e_eof"
            }
            if strings.Count(o.Err.(*bytes.Buffer).String(), c.output.locale.Text(key)) != 1 {
                t.Fatal(o.Err)
            }
        } else if err == nil {
            t.Fatal(mode, "missing error")
        }
        if mode == "end-disconnect" && (!receipt.ended || len(uxELogActions(result, receipt, err, true)) != 0 || len(remote.requests) != 1) {
            t.Fatal(result, receipt, err)
        }
        if mode == "exhausted" && (!receipt.exhausted || len(uxELogActions(result, receipt, err, true)) != 1) {
            t.Fatal(result, receipt, err)
        }
        if o.Out.(*bytes.Buffer).String() != "first 中文\n" || remote.controls != 0 {
            t.Fatal(o.Out, remote.controls)
        }
    }
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
            data, err := c.uxEDirectLogs(context.Background())
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
    actions := uxELogActions(result, uxELogReceipt{}, cause, true)
    if len(actions) != 1 || !reflect.DeepEqual(actions[0].Command, []string{"loaf", "logs", id, "-f", "--resume", token}) || !actions[0].RequiresConfirmation {
        t.Fatal(actions)
    }
    for _, blocked := range []error{io.ErrClosedPipe, context.Canceled, fault.New("LOG_CONSUMER_FAILED", "log_consumer_failed", nil), fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Permission denied"})} {
        if actions := uxELogActions(result, uxELogReceipt{}, blocked, true); len(actions) != 0 {
            t.Fatal(actions, blocked)
        }
    }
    if len(uxELogActions(result, uxELogReceipt{}, cause, false)) != 0 {
        t.Fatal("pipe was acknowledged")
    }
    result.View.Log.Cursor = strings.Repeat("a", 8193)
    if len(uxELogActions(result, uxELogReceipt{}, cause, true)) != 0 {
        t.Fatal("invalid cursor offered")
    }
    for _, req := range []wire.Request{
        {Version: wire.Version, Root: details.RemoteDir, ProjectID: "another-project", RunID: id},
        {Version: wire.Version, Root: "/another/root", ProjectID: details.ProjectID, RunID: id},
    } {
        result.View.Log.Cursor = uxECursor(req, 4)
        if len(uxELogActions(result, uxELogReceipt{}, cause, true)) != 0 {
            t.Fatal("cursor changed binding", req)
        }
    }
}

func TestUXENativeOwnershipReleasedAfterRead(t *testing.T) {
    c, _, _, id := uxEFixture(t, "after\n", "en")
    _, _, err := c.uxEReadLogs(context.Background(), client.LogsInput{ID: id, Follow: true})
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

func TestUXEMenuStreamCancellationAndBrokenConsumerDoNotPromptAgain(t *testing.T) {
    for _, mode := range []string{"cancel", "consumer"} {
        c, remote, o, id := uxEFixture(t, "2\n\n", "en")
        if mode == "cancel" {
            remote.mode = mode
        } else {
            c.output.out = &uxEFailWriter{cause: io.ErrClosedPipe}
        }
        _, err := c.uxELogs(context.Background(), id)
        var f *fault.Error
        want := "OBSERVATION_CANCELED"
        if mode == "consumer" {
            want = "LOG_CONSUMER_FAILED"
        }
        if !errors.As(err, &f) || f.Code != want || len(remote.requests) != 1 || remote.controls != 0 || strings.Contains(o.Err.(*bytes.Buffer).String(), c.output.locale.Text("ux_e_recovery_choice")) {
            t.Fatal(err, remote.requests, o.Err)
        }
        if mode == "consumer" && !strings.Contains(o.Err.(*bytes.Buffer).String(), "7") {
            t.Fatal("verified exit was lost", o.Err)
        }
    }
}

func TestUXEPublicRouteConsumesExplicitIDWithoutParentTarget(t *testing.T) {
    c, remote, _, id := uxEFixture(t, "2\n\n", "en")
    target := "another-default"
    c.grammar.Target = &target
    if !c.uxRouteAvailable("logs") {
        t.Fatal("00 has not consumed the logs route")
    }
    if data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "logs", ID: id}); data != nil || err != nil {
        t.Fatal(data, err)
    }
    if len(remote.requests) != 1 || remote.requests[0].RunID != id || remote.targets[0].Destination != "lab" || c.grammar.Target != &target {
        t.Fatal(remote.requests, remote.targets, c.grammar.Target)
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
