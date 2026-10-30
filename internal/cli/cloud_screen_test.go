package cli

import (
    "bytes"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
)

func exportCloudScreen(t *testing.T, name, rendered string) {
    t.Helper()
    if dir := os.Getenv("LOAF_UI_EXPORT"); dir != "" {
        if err := os.MkdirAll(dir, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(rendered), 0600); err != nil {
            t.Fatal(err)
        }
    }
}

func TestCloudHumanRecoveryIsOneResultNotReceiptDump(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        t.Run(lang, func(t *testing.T) {
            o, p := uxFixture(t)
            o.Operations = &fakeOperations{err: errors.New("fixture SSH connection failed")}
            o.TerminalOutput = true
            code, out, diagnostic := run(t, o, "--lang", lang, "cloud", "acquire", "test", "--offer", "wanted", "--image", "base-1", "--max-compute-hourly-price", "3.00", "--yes", "--progress=none")
            plain := ansi.Strip(out)
            if code != 1 || diagnostic != "" || p.creates != 1 || strings.Count(plain, "only-instance") != 1 || strings.Count(plain, "loaf cloud bind --request ") != 1 {
                t.Fatal(code, out, diagnostic, p.creates)
            }
            locale, _ := i18n.New(lang, nil)
            coverage := locale.Text("cloud_ui_coverage_partial", map[string]any{"Count": 100})
            for _, noise := range []string{"false", "allocation_attempts", "base-1", "intent", coverage, "镜像不可用", "price_unknown"} {
                if strings.Contains(plain, noise) {
                    t.Fatal("implementation detail in result", noise, plain)
                }
            }
            for _, line := range strings.Split(plain, "\n") {
                if ansi.StringWidth(line) > 80 {
                    t.Fatal("result overflows terminal", line)
                }
            }
            exportCloudScreen(t, "connection-"+lang, out)
        })
    }
}

func TestCloudApprovalDoesNotRepeatPlanInTranscript(t *testing.T) {
    o, p := uxFixture(t)
    o.Interactive = true
    o.In = strings.NewReader("yes\n")
    code, out, diagnostic := run(t, o, "-i", "--lang", "zh-Hans", "cloud", "acquire", "test", "--image", "base-1", "--gpu-model", "RTX 4090", "--max-compute-hourly-price", "3.00", "--continuous", "--progress=none")
    if code != 0 || p.creates != 1 || strings.Count(diagnostic, "RTX 4090") != 1 || strings.Count(diagnostic, "仅授权一台") != 1 || strings.Contains(diagnostic, "standard") || strings.Contains(out, "base-1") {
        t.Fatal(code, out, diagnostic)
    }
}

func TestCloudConnectingViewDropsInventoryAndKeepsRecoveryDetails(t *testing.T) {
    l, _ := i18n.New("zh-Hans", nil)
    call := &invocation{output: &presenter{locale: l, err: &bytes.Buffer{}, command: "cloud acquire"}, grammar: &grammar{}}
    plan := cloud.Plan{Product: "standard", Continuous: true, Requirements: cloud.Requirements{GPUModels: []string{"RTX 4090", "RTX 4090 D"}, GPUCount: 1, MaxComputeHourlyPrice: &cloud.Money{Amount: "3.00", Currency: "CNY"}}, AutoDL: cloud.ImageOptions{Image: "fixture-image-id", Name: "PyTorch / 2.8 / Python 3.12 / CUDA 12.8"}}
    f := &cloudFeedback{call: call, mode: "plain", started: time.Now().Add(-84 * time.Second), plan: &plan, account: "demo", target: "test", phase: "waiting", phaseAt: time.Now().Add(-84 * time.Second), polls: 7, request: "c_fixture_request", observation: &cloud.SearchResult{Rejected: map[string][]string{"a": {"no_stock", "price_unknown"}, "b": {"no_stock", "price_unknown"}}, InventoryPage: cloud.InventoryPage{Examined: 100, Complete: false, ObservedAt: time.Now()}}}
    next := time.Now().Add(8 * time.Second)
    waiting := f.frame("waiting", &next)
    before := waiting.Document.Render(80, true, true)
    if strings.Contains(before, "c_fixture_request") || strings.Contains(before, "fixture-image-id") || strings.Contains(before, "false") {
        t.Fatal(before)
    }
    if strings.Contains(waiting.Detail, l.Text("cloud_reject_price_unknown")) {
        t.Fatal("unquoted out-of-stock candidates mislabeled as price failures", waiting.Detail)
    }
    waiting.Document.Footer = waiting.Footer
    exportCloudScreen(t, "waiting-zh-Hans", waiting.Document.Render(80, true, true))
    ref := cloud.Ref{Provider: "autodl", Product: "standard", Account: "demo", ID: "fixture-instance"}
    if err := f.emit(cloud.Event{Type: "connecting", Instance: &ref}); err != nil {
        t.Fatal(err)
    }
    connected := f.frame("connecting", nil)
    view := connected.Document.Render(80, false, true)
    for _, stale := range []string{"100", l.Text("cloud_reject_no_stock"), "RTX 4090", "fixture-image-id", "c_fixture_request"} {
        if strings.Contains(view, stale) {
            t.Fatal("stale inventory in connection view", view)
        }
    }
    if !strings.Contains(connected.Detail, ref.ID) || !strings.Contains(connected.Detail, f.request) {
        t.Fatal(connected.Detail)
    }
    if !connected.NextAt.IsZero() {
        t.Fatal("inventory countdown survived phase transition")
    }
}

func TestCloudDeclinedDoesNotDumpApprovedConditions(t *testing.T) {
    o, p := uxFixture(t)
    o.Interactive = true
    o.In = strings.NewReader("no\n")
    code, out, _ := run(t, o, "-i", "--lang", "zh-Hans", "cloud", "acquire", "test", "--image", "base-1", "--gpu-model", "RTX 4090", "--max-compute-hourly-price", "3.00")
    if code != 2 || p.creates != 0 || !strings.Contains(out, "已取消租用") || strings.Contains(out, "base-1") || strings.Contains(out, "RTX 4090") {
        t.Fatal(code, out, p.creates)
    }
}

func TestCloudResultOutputFailureDoesNotReportSuccess(t *testing.T) {
    l, _ := i18n.New("en", nil)
    p := &presenter{locale: l, out: &failAfterWriter{}, err: &bytes.Buffer{}, command: "cloud bind"}
    if p.finish(cloudBinding{Target: "gpu", Status: "ready"}, nil) == 0 {
        t.Fatal("lost output accepted as success")
    }
}

func TestCloudLifecycleResultRetainsObservedState(t *testing.T) {
    ref := cloud.Ref{Provider: "autodl", Product: "standard", Account: "demo", ID: "instance"}
    for _, tc := range []struct {
        command, want, absent string
        data                  any
        err                   error
    }{
        {"cloud stop", "关机已完成", "接入尚未完成", cloud.Receipt{Instance: &ref, Status: "completed"}, nil},
        {"cloud start", "开机请求被拒绝", "结果待核对", cloud.Receipt{Operation: "start", Instance: &ref, Status: "rejected"}, fault.New("CLOUD_FAILED", "cloud_failed", nil)},
        {"cloud reconcile", "已确认未创建实例", "format_version", cloud.Receipt{Operation: "acquire", Status: "not_created"}, nil},
        {"cloud bind", "接入尚未完成", "可能正在计费", cloudBinding{Target: "gpu", Instance: ref, Status: "not_bound"}, fault.New("CLOUD_AUTH_REQUIRED", "cloud_auth_required", nil)},
    } {
        t.Run(tc.command+tc.want, func(t *testing.T) {
            l, _ := i18n.New("zh-Hans", nil)
            var out bytes.Buffer
            p := &presenter{locale: l, out: &out, err: &bytes.Buffer{}, command: tc.command}
            p.finish(tc.data, tc.err)
            if !strings.Contains(out.String(), tc.want) || strings.Contains(out.String(), tc.absent) {
                t.Fatal(out.String())
            }
        })
    }
}

func TestCloudFiniteOutputUsesColorOnlyOnCapableTerminal(t *testing.T) {
    for _, tc := range []struct {
        name     string
        terminal bool
        env      map[string]string
    }{{"pipe", false, nil}, {"no-color", true, map[string]string{"NO_COLOR": "1"}}, {"dumb", true, map[string]string{"TERM": "dumb"}}} {
        t.Run(tc.name, func(t *testing.T) {
            o, _ := uxFixture(t)
            o.TerminalOutput, o.Env = tc.terminal, tc.env
            code, out, diagnostic := run(t, o, "cloud", "acquire", "gpu", "--image", "base-1", "--yes", "--progress=none")
            if code != 0 || diagnostic != "" || strings.Contains(out, "\x1b") {
                t.Fatal(code, out, diagnostic)
            }
        })
    }
}
