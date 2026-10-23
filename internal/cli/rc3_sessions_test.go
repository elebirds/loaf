package cli

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/wire"
)

type checkpointRemote struct {
    runRemote
    token string
}

func (r *checkpointRemote) Follow(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool, emit func(wire.LogEvent) error) error {
    r.token, _ = wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: req.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, FileID: "1:2", Offset: 4, Boundary: strings.Repeat("a", 64)})
    return emit(wire.LogEvent{Type: "end", Record: r.record(req), Log: wire.LogChunk{Text: "log\n", Offset: 4, Cursor: r.token}})
}
func TestCursorFileRequiresConsumerAcknowledgementOutsideTerminal(t *testing.T) {
    for _, mode := range []string{"json", "pipe", "terminal"} {
        t.Run(mode, func(t *testing.T) {
            o, _ := runFixture(t)
            remote := &checkpointRemote{}
            o.Runs.Remote = remote
            o.TerminalOutput = mode == "terminal"
            code, out, stderr := run(t, o, "run", "--json", "--", "true")
            if code != 0 {
                t.Fatal(out, stderr)
            }
            var r struct {
                Data execution.View `json:"data"`
            }
            _ = json.Unmarshal([]byte(out), &r)
            p := filepath.Join(t.TempDir(), "cursor.json")
            args := []string{"logs", r.Data.ID, "-f", "--resume-file", p}
            if mode == "json" {
                args = append(args, "--json")
            }
            code, out, stderr = run(t, o, args...)
            if code != 0 {
                t.Fatal(code, out, stderr)
            }
            _, err := os.Stat(p)
            if (err == nil) != (mode == "terminal") {
                t.Fatal("incorrect acknowledgement", mode, err)
            }
            o.Runs = nil
            code, out, stderr = run(t, o, "logs", r.Data.ID, "--resume-file", p, "--ack-cursor", remote.token, "--json")
            if code != 0 || stderr != "" || !strings.Contains(out, `"acknowledged":true`) {
                t.Fatal(code, out, stderr)
            }
            if token, err := config.ReadLogCheckpoint(p); err != nil || token != remote.token {
                t.Fatal(token, err)
            }
        })
    }
}
func TestWaitCLIAndOfflineObservations(t *testing.T) {
    o, remote, refs := psFixture(t)
    o.Runs.Observations = o.Store
    remote.failID = refs[0].ID
    code, out, stderr := run(t, o, "wait", refs[0].ID, refs[1].ID, "--timeout", "20ms", "--json")
    if code != 1 || stderr != "" || !strings.Contains(out, `"completed":1`) || !strings.Contains(out, `"query_errors":1`) {
        t.Fatal(code, out, stderr)
    }
    o.Runs = nil
    code, out, stderr = run(t, o, "inspect", refs[1].ID, "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, `"last_observation"`) || !strings.Contains(out, `"status":"unknown"`) {
        t.Fatal(code, out, stderr)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr = run(t, o, "ps", "--local", "--lang", lang)
        if code != 0 || stderr != "" || out == "" {
            t.Fatal(code, out, stderr)
        }
    }
}
