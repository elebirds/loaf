package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

func TestRunNextActionsMatchAvailabilityAndSubmission(t *testing.T) {
    now := time.Now()
    const id = "r_000000000000000000000001"
    for _, tc := range []struct {
        name string
        view execution.View
        want [][]string
    }{
        {name: "running", view: execution.View{Status: "running"}, want: [][]string{{"loaf", "status", id}, {"loaf", "logs", id, "-f"}}},
        {name: "succeeded", view: execution.View{Status: "succeeded"}, want: [][]string{{"loaf", "pull", id}}},
        {name: "failed", view: execution.View{Status: "failed"}, want: [][]string{{"loaf", "logs", id}}},
        {name: "preparing", view: execution.View{Submission: "preparing", Status: "unknown"}, want: [][]string{{"loaf", "inspect", id}}},
        {name: "unconfirmed", view: execution.View{Submission: "uncertain", Status: "running"}, want: [][]string{{"loaf", "status", id}}},
        {name: "query-failed", view: execution.View{Status: "unknown", QueryError: "SSH_FAILED"}, want: [][]string{{"loaf", "status", id}}},
        {name: "cleaned-success", view: execution.View{Status: "succeeded", CleanedAt: &now}},
        {name: "partial-deletion", view: execution.View{Status: "succeeded", CleanupPending: true}},
        {name: "abandoned", view: execution.View{Abandonment: &task.RunAbandonment{RequestedAt: now}}},
    } {
        t.Run(tc.name, func(t *testing.T) {
            tc.view.ID = id
            actions, err := runNextActions(tc.view)
            if err != nil || len(actions) != len(tc.want) {
                t.Fatal(actions, err)
            }
            for i, action := range actions {
                if !reflect.DeepEqual(action.Command, tc.want[i]) || action.RequiresConfirmation {
                    t.Fatal(action, tc.want[i])
                }
            }
            for _, lang := range []string{"en", "zh-Hans"} {
                locale, err := i18n.New(lang, nil)
                if err != nil {
                    t.Fatal(err)
                }
                out, diagnostic := &bytes.Buffer{}, &bytes.Buffer{}
                p := &presenter{command: "status", locale: locale, out: out, err: diagnostic}
                if code := p.finish(tc.view, nil); code != 0 || diagnostic.Len() != 0 {
                    t.Fatal(code, out, diagnostic)
                }
                for _, argv := range tc.want {
                    display, _ := uxJCommandDisplay(argv)
                    if strings.Count(out.String(), display) != 1 {
                        t.Fatal("missing or repeated action", argv, out)
                    }
                }
                if len(tc.want) == 0 && strings.Contains(out.String(), "loaf ") {
                    t.Fatal("reclaimed payload suggested a data operation", out)
                }
            }
        })
    }
}

func TestRunAndSingleStatusJSONShareSuggestedArgv(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        o, remote := runFixture(t)
        for _, args := range [][]string{{"run", "--", "fixture-command"}, {"status"}} {
            if args[0] == "status" {
                args = append(args, remote.req.RunID)
            }
            code, out, diagnostic := run(t, o, append([]string{"--lang", lang, "--json"}, args...)...)
            var result struct {
                Data        execution.View `json:"data"`
                NextActions []nextAction   `json:"next_actions"`
            }
            if err := json.Unmarshal([]byte(out), &result); err != nil || code != 0 || diagnostic != "" {
                t.Fatal(code, out, diagnostic, err)
            }
            if len(result.NextActions) != 1 || !reflect.DeepEqual(result.NextActions[0].Command, []string{"loaf", "logs", result.Data.ID}) {
                t.Fatal("failed task must suggest its own log, not a new submission", out)
            }
        }
    }
}

func TestBatchWarningsOnlyReportActualGaps(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        locale, _ := i18n.New(lang, nil)
        for _, tc := range []struct {
            data any
            warn bool
        }{
            {data: runList{}},
            {data: execution.WaitResult{}},
            {data: runList{StateErrors: 1}, warn: true},
            {data: runList{Eligible: 2, Examined: 1}, warn: true},
            {data: execution.WaitResult{StateErrors: 1}, warn: true},
            {data: execution.WaitResult{Unexamined: 1}, warn: true},
        } {
            out := &bytes.Buffer{}
            if err := printResult(out, tc.data, locale, true); err != nil {
                t.Fatal(err)
            }
            label := "Local save failures:"
            if lang == "zh-Hans" {
                label = "本地保存失败："
            }
            if strings.Contains(out.String(), label) != tc.warn {
                t.Fatal(tc.data, out)
            }
        }
    }
}

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
    if code, out, stderr := run(t, o, "machine", "add", "lab", "--ssh", "lab", "--json"); code != 0 {
        t.Fatal(out, stderr)
    }
    if code, out, stderr := run(t, o, "machine", "use", "lab", "--json"); code != 0 {
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
        for _, command := range []string{"run", "status", "logs"} {
            if code, out, stderr := run(t, o, "--lang", lang, command, "--help"); code != 0 || out == "" || stderr != "" {
                t.Fatal(command, out, stderr)
            } else if command == "logs" && strings.Contains(out, "--target") {
                t.Fatal("query help advertises target redirection", out)
            }
        }
    }
    for _, args := range [][]string{{"run", "python"}, {"run", "--wait", "-1s"}, {"status", "--wait", "1s"}, {"status", "--limit", "0"}, {"logs", "--tail", "-1"}, {"logs", "--target", "lab"}} {
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
    code, out, stderr := run(t, o, "status", "--json")
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
    if code, out, stderr = run(t, o, "status", "--json"); code != 0 || stderr != "" || !strings.Contains(out, fmt.Sprintf(`"total":%d`, 2)) {
        t.Fatal(code, out, stderr)
    }
}
