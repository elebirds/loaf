package cli

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

type runChecker struct{}

func (runChecker) Check(context.Context, config.Target, string, bool) (ssh.Report, error) {
    return ssh.Report{Arch: "aarch64", RemoteDir: "/remote/project"}, nil
}

type runSender struct{}

func (runSender) Sync(context.Context, config.Target, string, string, ssh.SyncInput, bool) (ssh.Transfer, error) {
    return ssh.Transfer{}, nil
}

type runRemote struct {
    req    wire.Request
    failID string
}

func (r *runRemote) Control(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (*wire.RunRecord, error) {
    r.req = req
    return r.record(req), nil
}
func (r *runRemote) record(req wire.Request) *wire.RunRecord {
    code := 7
    return &wire.RunRecord{FormatVersion: 1, Request: req, Status: "failed", Phase: "command", SupervisorPID: 123, ExitCode: &code}
}
func (r *runRemote) Observe(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    if req.RunID == r.failID {
        return wire.Observation{}, fault.New("SSH_FAILED", "ssh_failed", nil)
    }
    return wire.Observation{Record: r.record(req), Log: wire.LogChunk{Text: "中文 log\n", Offset: 11}}, nil
}
func (r *runRemote) Follow(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool, emit func(wire.LogEvent) error) error {
    if err := emit(wire.LogEvent{Type: "chunk", Record: r.record(req), Log: wire.LogChunk{Text: "中文 log\n", Offset: 11}}); err != nil {
        return err
    }
    return emit(wire.LogEvent{Type: "end", Record: r.record(req), Log: wire.LogChunk{Offset: 11}})
}

func runFixture(t *testing.T) (testOptions, *runRemote) {
    t.Helper()
    o := fixture(t)
    remote := &runRemote{}
    o.Runs = &execution.Service{Index: o.State, Check: runChecker{}, Runner: func(context.Context, config.Target, string, bool) (string, error) { return "/remote/runner", nil }, Send: runSender{}, Remote: remote}
    if code, out, stderr := run(t, o, "add", "lab", "--ssh", "lab", "--json"); code != 0 {
        t.Fatal(out, stderr)
    }
    if code, out, stderr := run(t, o, "use", "lab", "--json"); code != 0 {
        t.Fatal(out, stderr)
    }
    return o, remote
}

func TestRunPublicContractsAndLiteralArgv(t *testing.T) {
    o, remote := runFixture(t)
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "run", "--wait", "1s", "--json", "--", "python", "a b.py", "--lang", "not-a-cli-flag")
        if code != 0 || stderr != "" {
            t.Fatal(code, out, stderr)
        }
        var response struct {
            OK   bool           `json:"ok"`
            Data execution.View `json:"data"`
        }
        if err := json.Unmarshal([]byte(out), &response); err != nil || !response.OK || response.Data.Status != "failed" || response.Data.ExitCode == nil || *response.Data.ExitCode != 7 {
            t.Fatal(out, err)
        }
        if len(remote.req.Args) != 4 || remote.req.Args[1] != "a b.py" || remote.req.Args[3] != "not-a-cli-flag" {
            t.Fatal(remote.req)
        }
        code, out, stderr = run(t, o, "--lang", lang, "logs", "-f", "--json")
        if code != 0 || stderr != "" || len(strings.Split(strings.TrimSpace(out), "\n")) != 2 || !strings.Contains(out, `"type":"end"`) {
            t.Fatal(code, out, stderr)
        }
        for _, command := range []string{"run", "ps", "logs"} {
            if code, out, stderr := run(t, o, "--lang", lang, command, "--help"); code != 0 || out == "" || stderr != "" {
                t.Fatal(command, out, stderr)
            } else if command == "logs" && strings.Contains(out, "--target") {
                t.Fatal("query help advertises target redirection", out)
            }
        }
    }
    for _, args := range [][]string{{"run", "python"}, {"run", "--wait", "-1s"}, {"ps", "--wait", "1s"}, {"ps", "--limit", "0"}, {"logs", "--tail", "-1"}, {"logs", "--target", "lab"}} {
        code, out, stderr := run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatal(args, code, out, stderr)
        }
    }
}

func TestPartialStatusKeepsSuccessfulRows(t *testing.T) {
    o, remote := runFixture(t)
    for i := 0; i < 2; i++ {
        if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
            t.Fatal(code, out, stderr)
        }
    }
    remote.failID = remote.req.RunID
    code, out, stderr := run(t, o, "ps", "--json")
    var response struct {
        Data  runList   `json:"data"`
        Error errorView `json:"error"`
    }
    if err := json.Unmarshal([]byte(out), &response); err != nil || code != 1 || stderr != "" || response.Error.Code != "QUERY_PARTIAL" || len(response.Data.Runs) != 2 {
        t.Fatal(code, out, stderr, err)
    }
    successes, unreachable := 0, 0
    for _, v := range response.Data.Runs {
        if v.QueryError != "" {
            unreachable++
            if v.Status != "unknown" {
                t.Fatal(v)
            }
        } else {
            successes++
        }
    }
    if successes != 1 || unreachable != 1 {
        t.Fatal(out)
    }
}

func TestHumanWaitStreamsWithoutDuplicateTail(t *testing.T) {
    o, _ := runFixture(t)
    code, out, stderr := run(t, o, "--lang", "zh-Hans", "run", "--wait", "1s", "--", "python", "train.py")
    if code != 0 || strings.Count(out, "中文 log") != 1 || !strings.Contains(out, "失败") || !strings.Contains(stderr, "正在同步") || !strings.Contains(stderr, "远端已接管") {
        t.Fatal(code, out, stderr)
    }
    // Project wait preferences are honored, explicit 0s overrides them.
    os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[run]\nwait = '1s'\ncommand = 'python train.py'\n"), 0600)
    code, out, stderr = run(t, o, "run", "--wait", "0s", "--json")
    if code != 0 || stderr != "" || strings.Contains(out, `"log"`) {
        t.Fatal(code, out, stderr)
    }
    code, out, stderr = run(t, o, "logs", "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, "中文 log") {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr = run(t, o, "ps", "--json"); code != 0 || stderr != "" || !strings.Contains(out, fmt.Sprintf(`"total":%d`, 2)) {
        t.Fatal(code, out, stderr)
    }
}
