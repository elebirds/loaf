package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
)

func TestNotificationKindDiscoveryMatchesImplementedAdapters(t *testing.T) {
    kinds := []string{"codex", "codex-desktop", "claude-channel", "webhook"}
    for _, language := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        notify := &fakeNotify{}
        o.Notify = notify
        code, out, diagnostic := run(t, o, "help", "notify", "destination", "add", "--lang", language, "--json")
        var help struct {
            Data helpResult `json:"data"`
        }
        if code != 0 || diagnostic != "" || json.Unmarshal([]byte(out), &help) != nil || strings.Contains(out, "claude-resume") {
            t.Fatal(code, out, diagnostic)
        }
        var advertised []string
        for _, flag := range help.Data.Flags {
            if flag.Name == "kind" {
                advertised = flag.Enum
            }
        }
        if !reflect.DeepEqual(advertised, kinds) {
            t.Fatal(advertised)
        }
        for _, kind := range kinds {
            endpoint := "/private/tmp/loaf-test-endpoint"
            if kind == "webhook" {
                endpoint = "http://127.0.0.1/loaf"
            }
            code, out, diagnostic = run(t, o, "notify", "destination", "add", "results", "--kind", kind, "--session", "session_1", "--endpoint", endpoint, "--lang", language, "--json")
            if code != 0 || diagnostic != "" || notify.destination.Destination.Kind != kind {
                t.Fatal(kind, code, out, diagnostic)
            }
        }
        for _, invalid := range [][]string{{"--kind", "claude-resume"}, {"--kind=unknown-private-value"}} {
            notify.destination = client.DestinationInput{}
            args := append([]string{"notify", "destination", "add", "results"}, invalid...)
            args = append(args, "--session", "session_1", "--lang", language, "--json")
            code, out, diagnostic = run(t, o, args...)
            var result struct {
                Error errorView `json:"error"`
            }
            if code != 2 || diagnostic != "" || json.Unmarshal([]byte(out), &result) != nil || result.Error.Code != "BAD_CONFIG" || result.Error.Reason != "invalid_value" || result.Error.Argument != "--kind" || notify.destination.Name != "" {
                t.Fatal(invalid, code, out, diagnostic, notify.destination)
            }
            if !reflect.DeepEqual(result.Error.HelpCommand, []string{"loaf", "help", "notify", "destination", "add"}) || !strings.Contains(result.Error.Message, strings.Join(kinds, ", ")) || strings.Contains(out, "unknown-private-value") || strings.Contains(result.Error.Message, "claude-resume") {
                t.Fatal(result.Error, out)
            }
        }
    }
}

func TestDisabledQueueExplainsExplicitEnablement(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        q := &fakeQueue{err: fault.At(fault.New("QUEUE_NOT_ENABLED", "queue_not_enabled", nil), "queue")}
        o.Queue = q
        code, out, diagnostic := run(t, o, "queue", "list", "--lang", language, "--json")
        var result struct {
            Error errorView `json:"error"`
        }
        if code != 1 || diagnostic != "" || json.Unmarshal([]byte(out), &result) != nil || result.Error.Code != "QUEUE_NOT_ENABLED" || result.Error.Stage != "queue" || !strings.Contains(result.Error.Message, "loaf queue enable --slots N") || !strings.Contains(result.Error.Message, "loaf run") || q.enabled.Slots != 0 {
            t.Fatal(code, out, diagnostic, q.enabled)
        }
        code, out, diagnostic = run(t, o, "queue", "list", "--lang", language)
        if code != 1 || !strings.Contains(diagnostic, result.Error.Message) || strings.Contains(diagnostic, "saved request") || strings.Contains(diagnostic, "已保存的请求") || q.enabled.Slots != 0 {
            t.Fatal(code, out, diagnostic, q.enabled)
        }
    }
}

func TestTerminalRunStageKeepsOutcomeAndNextActionClear(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        locale, _ := i18n.New(language, nil)
        for _, tc := range []struct {
            phase, status, reason string
            exit                  int
        }{
            {phase: "command", status: "succeeded", exit: 0},
            {phase: "command", status: "failed", exit: 7},
            {phase: "setup", status: "failed", reason: "EXEC_FAILED", exit: 126},
            {phase: "setup", status: "stopped", exit: 143},
            {phase: "copying", status: "failed", reason: "COPY_FAILED", exit: 1},
            {phase: "starting", status: "failed", reason: "SUPERVISOR_START_FAILED", exit: 1},
        } {
            action := "loaf logs r_terminal"
            if tc.status == "succeeded" {
                action = "loaf pull r_terminal"
            }
            phase := locale.Text("run_phase_" + tc.phase)
            view := execution.View{ID: "r_terminal", Target: "lab", Status: tc.status, Submission: "submitted", Phase: tc.phase, Reason: tc.reason, ExitCode: &tc.exit}
            out, diagnostic := &bytes.Buffer{}, &bytes.Buffer{}
            p := &presenter{command: "run", locale: locale, out: out, err: diagnostic}
            if code := p.finish(view, nil); code != 0 || diagnostic.Len() != 0 || !strings.Contains(out.String(), localizedRunState(tc.status, locale)) || !strings.Contains(out.String(), phase) || !strings.Contains(out.String(), locale.Text("run_exit", map[string]any{"Value": tc.exit})) || !strings.Contains(out.String(), action) {
                t.Fatal(code, out, diagnostic)
            }
            if tc.reason != "" && !strings.Contains(out.String(), locale.Text("run_reason_"+tc.reason)) {
                t.Fatal("terminal result lost its reason", tc, out)
            }
            out.Reset()
            printRunRow(out, view, locale)
            if !strings.Contains(out.String(), phase) {
                t.Fatal(out)
            }
            for _, active := range []string{"running command", "running setup", "copying source", "starting supervisor", "执行命令", "准备环境", "复制源码", "启动监督进程"} {
                if strings.Contains(phase, active) {
                    t.Fatal("terminal stage claims active execution", tc, phase)
                }
            }
        }
        out := &bytes.Buffer{}
        printRun(out, execution.View{ID: "r_preparing", Status: "unknown", Submission: "preparing", Phase: "prepared", Reason: "COPY_FAILED"}, locale)
        if !strings.Contains(out.String(), locale.Text("run_phase_prepared")) || !strings.Contains(out.String(), locale.Text("run_reason_COPY_FAILED")) {
            t.Fatal("preparation failure lost its stage", out)
        }
    }
}

func TestCurrentSessionUnavailableExplainsSandboxPermission(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        locale, _ := i18n.New(language, nil)
        p := &presenter{locale: locale}
        view := p.error(fault.New("CURRENT_SESSION_UNAVAILABLE", "notify_current_unavailable", nil))
        if view.Code != "CURRENT_SESSION_UNAVAILABLE" || !strings.Contains(view.Message, "IPC") {
            t.Fatal(view)
        }
        if language == "en" && (!strings.Contains(view.Message, "If the tool sandbox") || !strings.Contains(view.Message, "this chat")) || language == "zh-Hans" && (!strings.Contains(view.Message, "若工具沙箱") || !strings.Contains(view.Message, "原会话")) {
            t.Fatal(view)
        }
    }
}

type queueSubmissionFixture struct {
    fakeQueue
    result client.QueueSubmitResult
}

func (q *queueSubmissionFixture) Submit(context.Context, client.QueueSubmitInput, client.ProgressFunc) (client.QueueSubmitResult, error) {
    return q.result, q.err
}

func TestQueueSubmissionSuggestsSavedRequestBeforeRunWaiting(t *testing.T) {
    request := &queue.Request{ID: "q_original", RunID: "r_original", State: "queued"}
    preview := &execution.RunPreview{DryRun: true, Root: "/project", Target: "lab", Args: []string{"true"}, Tail: 100}
    preview.Source.Policy.Mode = "mirror"
    for _, language := range []string{"en", "zh-Hans"} {
        for _, tc := range []struct {
            name   string
            result client.QueueSubmitResult
            err    error
            action bool
        }{
            {name: "admitted", result: client.QueueSubmitResult{Stage: "admitted", Request: request}, action: true},
            {name: "saved-uncertain", result: client.QueueSubmitResult{Stage: "enqueueing", Request: request, NextAction: "inspect_original_request"}, err: fault.New("QUEUE_ENQUEUE_UNCONFIRMED", "queue_operation_failed", map[string]any{"Code": "QUEUE_ENQUEUE_UNCONFIRMED"}), action: true},
            {name: "unsaved", result: client.QueueSubmitResult{Stage: "saving_identity", Request: request, NextAction: "repair_local_state"}, err: fault.New("STATE_WRITE", "internal_error", nil)},
            {name: "preview", result: client.QueueSubmitResult{Stage: "planning", Preview: preview}},
        } {
            t.Run(language+"/"+tc.name, func(t *testing.T) {
                o := fixture(t)
                o.Queue = &queueSubmissionFixture{fakeQueue: fakeQueue{err: tc.err}, result: tc.result}
                _, out, diagnostic := run(t, o, "queue", "submit", "--lang", language, "--json", "--", "true")
                var result struct {
                    NextActions []nextAction `json:"next_actions"`
                }
                if diagnostic != "" || json.Unmarshal([]byte(out), &result) != nil || (len(result.NextActions) == 1) != tc.action {
                    t.Fatal(out, diagnostic)
                }
                if tc.action {
                    a := result.NextActions[0]
                    if a.Kind != "show_request" || a.RequiresConfirmation || !reflect.DeepEqual(a.Command, []string{"loaf", "queue", "show", "q_original"}) {
                        t.Fatal(a)
                    }
                }
                _, out, _ = run(t, o, "queue", "submit", "--lang", language, "--", "true")
                expected := 0
                if tc.action {
                    expected = 1
                }
                if strings.Count(out, "loaf queue show q_original") != expected || strings.Contains(out, "loaf wait") || strings.Contains(out, "queue_next_") {
                    t.Fatal(out)
                }
            })
        }
    }
}
