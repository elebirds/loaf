//go:build !windows

package cli

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "os"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/i18n"
)

type wizardTerminal struct {
    *os.File
    mu   sync.Mutex
    text bytes.Buffer
}

type countWizardCloud struct {
    *uxCloud
    plans chan cloud.Plan
}

func (p *countWizardCloud) Search(_ context.Context, plan cloud.Plan, _ int) (cloud.InventoryPage, error) {
    p.plans <- plan
    return cloud.InventoryPage{Offers: []cloud.Offer{}, Complete: true}, nil
}

func waitWizardText(t *testing.T, ctx context.Context, output *wizardTerminal, value string) {
    t.Helper()
    for {
        view := output.snapshot()
        if strings.Contains(view, value) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing %q in wizard:\n%s", value, view)
        case <-time.After(time.Millisecond):
        }
    }
}

func TestCloudWizardTerminalAcceptsOneGPU(t *testing.T) {
    for _, tc := range []struct {
        name, keys, invalid, product string
    }{
        {name: "default", keys: "\r"},
        {name: "surrounding-whitespace", keys: "\x15 1 \r"},
        {name: "full-width-digits", keys: "\x15　１　\r"},
        {name: "correct-zero", invalid: "0", keys: "\x151\r"},
        {name: "correct-negative", invalid: "-1", keys: "\x151\r"},
        {name: "correct-too-many-standard", invalid: "65", keys: "\x151\r"},
        {name: "correct-too-many-pro", invalid: "9", keys: "\x151\r", product: "pro"},
    } {
        t.Run(tc.name, func(t *testing.T) {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { master.Close(); terminal.Close() })
            drained := make(chan struct{})
            go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: 40, Cols: 80}); err != nil {
                t.Fatal(err)
            }
            o, base := uxFixture(t)
            provider := &countWizardCloud{uxCloud: base, plans: make(chan cloud.Plan, 1)}
            o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return provider, nil }
            output := &wizardTerminal{File: terminal}
            o.In, o.Err = terminal, output
            o.Interactive, o.TerminalError = true, true
            o.Env["TERM"] = "xterm-256color"
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            done, joined := make(chan int, 1), make(chan struct{})
            args := []string{"-i", "--lang", "zh-Hans", "cloud", "acquire"}
            if tc.product != "" {
                args = append(args, "--product", tc.product)
            }
            go func() {
                defer close(joined)
                done <- execute(ctx, args, o)
            }()
            t.Cleanup(func() { cancel(); <-joined })
            waitWizardText(t, ctx, output, "↑↓ 选择")
            if _, err := io.WriteString(master, "\r"); err != nil {
                t.Fatal(err)
            }
            for _, step := range []struct {
                title, keys string
            }{
                {"GPU 型号", "\r"},
                {"GPU 卡数", tc.keys},
                {"允许的地区", "\r"},
                {"配套资源条件", "\r"},
                {"下一步", "\r"},
            } {
                waitWizardText(t, ctx, output, "┃ "+step.title)
                if step.title == "GPU 卡数" && tc.invalid != "" {
                    if _, err := io.WriteString(master, "\x15"+tc.invalid+"\r"); err != nil {
                        t.Fatal(err)
                    }
                    l, _ := i18n.New("zh-Hans", nil)
                    maximum := 64
                    if tc.product == "pro" {
                        maximum = 8
                    }
                    waitWizardText(t, ctx, output, l.Text("cloud_gpu_count_invalid", map[string]any{"Count": maximum}))
                    select {
                    case plan := <-provider.plans:
                        t.Fatal("invalid card count reached the provider", plan)
                    default:
                    }
                }
                if _, err := io.WriteString(master, step.keys); err != nil {
                    t.Fatal(err)
                }
            }
            select {
            case plan := <-provider.plans:
                if plan.Requirements.GPUCount != 1 {
                    t.Fatal("displayed one card became a different request", plan)
                }
            case <-ctx.Done():
                t.Fatal("one GPU never reached the stock query", output.snapshot())
            }
            cancel()
            if code := <-done; code == 0 || provider.creates != 0 {
                t.Fatal("numeric input test unexpectedly acquired a resource", code, provider.creates)
            }
        })
    }
}

func (w *wizardTerminal) Write(p []byte) (int, error) {
    n, err := w.File.Write(p)
    w.mu.Lock()
    w.text.Write(p[:n])
    w.mu.Unlock()
    return n, err
}

func (w *wizardTerminal) snapshot() string {
    w.mu.Lock()
    defer w.mu.Unlock()
    return ansi.Strip(w.text.String())
}

// This exercises the real CLI entry page, not a separately assembled sample form.
func TestCloudWizardTerminalEntryShowsBothModesAndAllSteps(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, columns := range []uint16{80, 120} {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            // An actual terminal continuously consumes its output. Leaving the
            // PTY master unread can fill the kernel buffer and block the final
            // renderer flush during cancellation, especially under -race.
            drained := make(chan struct{})
            go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: 24, Cols: columns}); err != nil {
                t.Fatal(err)
            }
            o, provider := uxFixture(t)
            output := &wizardTerminal{File: terminal}
            o.In, o.Err = terminal, output
            o.Interactive, o.TerminalError = true, true
            o.Env["TERM"] = "xterm-256color"
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            done, joined := make(chan int, 1), make(chan struct{})
            go func() {
                defer close(joined)
                done <- execute(ctx, []string{"-i", "--lang", language, "cloud", "acquire", "--continuous"}, o)
            }()
            t.Cleanup(func() { cancel(); <-joined })
            l, _ := i18n.New(language, nil)
            for {
                view := output.snapshot()
                if strings.Contains(view, l.Text("cloud_mode_select")) && strings.Contains(view, l.Text("cloud_mode_auto")) && strings.Contains(view, "● "+l.Text("cloud_flow_mode")) && strings.Contains(view, "○ "+l.Text("cloud_flow_connection")) {
                    break
                }
                select {
                case <-ctx.Done():
                    t.Fatalf("%s %d columns: entry missing options or roadmap:\n%s", language, columns, view)
                case <-time.After(time.Millisecond):
                }
            }
            cancel()
            if code := <-done; code == 0 || provider.creates != 0 || provider.searches != 0 {
                t.Fatal("entry unexpectedly advanced", code, provider)
            }
        }
    }
}

func TestCloudAcquireRichReviewEndsAfterOneResult(t *testing.T) {
    for _, tc := range []struct {
        lang       string
        cols, rows int
    }{{"en", 80, 24}, {"zh-Hans", 120, 32}, {"zh-Hans", 80, 18}} {
        t.Run(fmt.Sprintf("%s-%dx%d", tc.lang, tc.cols, tc.rows), func(t *testing.T) {
            o, p := uxFixture(t)
            code, out, stderr := run(t, o, "cloud", "profile", "save", "train", "--gpu-model=RTX 4090", "--image=base-1", "--max-compute-hourly-price=3", "--continuous", "--json")
            if code != 0 {
                t.Fatal(out, stderr)
            }
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            _ = pty.Setsize(terminal, &pty.Winsize{Rows: uint16(tc.rows), Cols: uint16(tc.cols)})
            drained := make(chan struct{})
            go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
            output := &wizardTerminal{File: terminal}
            o.In, o.Err = terminal, output
            o.Interactive, o.TerminalError = true, true
            o.Env["TERM"] = "xterm-256color"
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            done, joined := make(chan int, 1), make(chan struct{})
            go func() {
                defer close(joined)
                done <- execute(ctx, []string{"-i", "--lang", tc.lang, "cloud", "acquire", "--profile=train"}, o)
            }()
            t.Cleanup(func() { cancel(); <-joined; terminal.Close(); master.Close(); <-drained })
            l, _ := i18n.New(tc.lang, nil)
            previous := 0
            for _, step := range []struct{ title, keys string }{{"cloud_wizard_name", "\r"}, {"cloud_wizard_navigation", "\r"}, {"cloud_ui_type_yes", "yes\r"}} {
                if step.title == "cloud_ui_type_yes" {
                    for !strings.Contains(output.snapshot()[previous:], "┃ >") {
                        select {
                        case <-ctx.Done():
                            t.Fatal(output.snapshot())
                        case <-time.After(time.Millisecond):
                        }
                    }
                } else if step.title == "cloud_wizard_navigation" {
                    for !strings.Contains(output.snapshot()[previous:], "↑↓") {
                        select {
                        case <-ctx.Done():
                            t.Fatal(output.snapshot())
                        case <-time.After(time.Millisecond):
                        }
                    }
                } else {
                    waitWizardText(t, ctx, output, l.Text(step.title))
                }
                if step.title == "cloud_ui_type_yes" {
                    exportCloudScreen(t, fmt.Sprintf("review-%s-%d-%d", tc.lang, tc.cols, tc.rows), output.raw())
                }
                previous = len(output.snapshot())
                _, _ = io.WriteString(master, step.keys)
            }
            select {
            case code := <-done:
                if code != 0 || p.creates != 1 {
                    t.Fatal(code, p.creates, output.snapshot())
                }
            case <-ctx.Done():
                t.Fatal(output.snapshot())
            }

        })
    }
}

func (w *wizardTerminal) raw() string { w.mu.Lock(); defer w.mu.Unlock(); return w.text.String() }
