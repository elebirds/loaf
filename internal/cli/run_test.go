package cli

import (
    "context"
    "encoding/json"
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
    if req.Action == "status" && req.RunID == r.failID {
        return nil, fault.New("SSH_FAILED", "ssh_failed", nil)
    }
    r.req = req
    return r.record(req), nil
}
func (r *runRemote) record(req wire.Request) *wire.RunRecord {
    code := 7
    return &wire.RunRecord{FormatVersion: 1, Request: req, Status: "failed", Phase: "command", SupervisorPID: 123, ExitCode: &code}
}
func runFixture(t *testing.T) (Options, *runRemote) {
    t.Helper()
    o := fixture(t)
    remote := &runRemote{}
    o.Runs = &execution.Service{Index: o.Store, Check: runChecker{}, Runner: func(context.Context, config.Target, string, bool) (string, error) { return "/remote/runner", nil }, Send: runSender{}, Remote: remote}
    if code, out, stderr := run(t, o, "add", "lab", "--ssh", "lab", "--json"); code != 0 {
        t.Fatal(out, stderr)
    }
    if code, out, stderr := run(t, o, "use", "lab", "--json"); code != 0 {
        t.Fatal(out, stderr)
    }
    return o, remote
}

func TestRunArgvConfigurationAndStatusContracts(t *testing.T) {
    o, remote := runFixture(t)
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "run", "--json", "--", "python", "a b.py", "--lang", "not-a-cli-flag")
        var response struct {
            OK   bool           `json:"ok"`
            Data execution.View `json:"data"`
        }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &response) != nil || !response.OK || response.Data.Status != "failed" || response.Data.ExitCode == nil || *response.Data.ExitCode != 7 {
            t.Fatal(code, out, stderr)
        }
        if len(remote.req.Args) != 4 || remote.req.Args[1] != "a b.py" || remote.req.Args[3] != "not-a-cli-flag" {
            t.Fatal(remote.req)
        }
        for _, command := range []string{"run", "ps"} {
            code, out, stderr := run(t, o, "--lang", lang, command, "--help")
            if code != 0 || out == "" || stderr != "" || command == "ps" && strings.Contains(out, "--target") {
                t.Fatal(command, code, out, stderr)
            }
        }
    }
    for _, args := range [][]string{{"run", "python"}, {"ps", "--limit", "0"}, {"ps", "--target", "lab"}} {
        code, out, stderr := run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatal(args, code, out, stderr)
        }
    }
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[run]\ncommand='python train.py'\nsetup='uv sync'\n[run.env]\nPRIVATE_VALUE='not-public'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    code, out, stderr := run(t, o, "run", "--json")
    if code != 0 || stderr != "" || remote.req.Setup != "uv sync" || remote.req.Env["PRIVATE_VALUE"] != "not-public" || strings.Contains(out, "not-public") {
        t.Fatal(code, out, stderr, remote.req)
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
