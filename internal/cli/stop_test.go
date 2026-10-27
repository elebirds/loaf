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
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

func TestStopSettingsAndPublicOutput(t *testing.T) {
    o, remote := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    id := remote.req.RunID
    base, err := os.ReadFile(o.Config.ConfigFile)
    if err != nil {
        t.Fatal(err)
    }
    globalGrace, projectGrace, zero := int64(30000), int64(2000), int64(0)
    for _, tc := range []struct {
        global, project string
        flags           []string
        want            *int64
    }{
        {},
        {global: "30s", want: &globalGrace},
        {global: "30s", project: "2s", want: &projectGrace},
        {global: "30s", project: "2s", flags: []string{"--grace", "0s"}, want: &zero},
    } {
        data := append([]byte(nil), base...)
        if tc.global != "" {
            data = append(data, []byte("\n[stop]\ngrace = '"+tc.global+"'\n")...)
        }
        if err := os.WriteFile(o.Config.ConfigFile, data, 0600); err != nil {
            t.Fatal(err)
        }
        text := ""
        if tc.project != "" {
            text = "[stop]\ngrace = '" + tc.project + "'\n"
        }
        if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(text), 0600); err != nil {
            t.Fatal(err)
        }
        for _, language := range []string{"en", "zh-Hans"} {
            args := append([]string{"--lang", language, "stop", "--json"}, tc.flags...)
            code, out, stderr := run(t, o, args...)
            var response struct {
                OK   bool           `json:"ok"`
                Data execution.View `json:"data"`
            }
            if err := json.Unmarshal([]byte(out), &response); err != nil || code != 0 || stderr != "" || !response.OK || response.Data.ID != id || response.Data.Status != "failed" || response.Data.ExitCode == nil || *response.Data.ExitCode != 7 || strings.Count(strings.TrimSpace(out), "\n") != 0 {
                t.Fatal(code, out, stderr, err)
            }
            if remote.req.Action != "stop" || remote.req.RunID != id || (remote.req.GraceMillis == nil) != (tc.want == nil) || tc.want != nil && *remote.req.GraceMillis != *tc.want {
                t.Fatal("wrong stop selection or policy", remote.req, tc)
            }
        }
    }
    for _, language := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", language, "stop", "--help")
        if code != 0 || stderr != "" || !strings.Contains(out, "--grace") || strings.Contains(out, "--target") {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "--lang", language, "stop", id)
        if code != 0 || !strings.Contains(out, id) || stderr == "" {
            t.Fatal(code, out, stderr)
        }
        if language == "zh-Hans" && (!strings.Contains(out, "失败") || !strings.Contains(stderr, "等待确认")) {
            t.Fatal(out, stderr)
        }
    }
    for _, args := range [][]string{{"stop", "one", "two"}, {"stop", "--target", "lab"}, {"stop", "--grace", "bad"}, {"stop", "--grace="}, {"stop", "--grace", "-1s"}, {"stop", "--grace", "6m"}} {
        code, out, stderr := run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" || !strings.Contains(out, `"code":"BAD_CONFIG"`) {
            t.Fatal(args, code, out, stderr)
        }
    }
}

type stopFailureRemote struct {
    execution.Remote
    code string
}

func (r stopFailureRemote) Control(context.Context, config.Target, string, wire.Request, bool) (*wire.RunRecord, error) {
    return nil, &ssh.RemoteError{Code: r.code}
}

func (r stopFailureRemote) Observe(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.Observation, error) {
    return wire.Observation{Record: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123}}, nil
}

func TestStopUncertaintyAndRejectedRequests(t *testing.T) {
    o, _ := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    for _, tc := range []struct {
        remote, want string
        exit         int
        flags        []string
    }{
        {"STOP_UNCONFIRMED", "STOP_UNCONFIRMED", 3, nil},
        {"BAD_REQUEST", "BAD_REQUEST", 1, []string{"--grace", "0s"}},
        {"RUN_NOT_FOUND", "RUN_NOT_FOUND", 1, nil},
    } {
        o.Runs.Remote = stopFailureRemote{code: tc.remote}
        for _, lang := range []string{"en", "zh-Hans"} {
            args := append([]string{"--lang", lang, "stop", "--json"}, tc.flags...)
            code, out, stderr := run(t, o, args...)
            if code != tc.exit || stderr != "" || !strings.Contains(out, `"code":"`+tc.want+`"`) || !strings.Contains(out, `"run_id":`) || strings.Contains(out, `"status":"stopped"`) {
                t.Fatal(code, out, stderr)
            }
        }
    }
}
