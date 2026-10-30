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
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
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

func TestCloudLifecycleRichReviewAndHub(t *testing.T) {
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
            done := make(chan int, 1)
            go func() {
                done <- execute(ctx, []string{"-i", "--lang", tc.lang, "cloud", "acquire", "--profile=train"}, o)
            }()
            t.Cleanup(func() { cancel(); terminal.Close(); master.Close(); <-drained })
            l, _ := i18n.New(tc.lang, nil)
            previous := 0
            for _, step := range []struct{ title, keys string }{{"cloud_wizard_name", "\r"}, {"cloud_wizard_after", "\r"}, {"cloud_wizard_navigation", "\r"}, {"cloud_ui_type_yes", "yes\r"}} {
                if step.title == "cloud_ui_type_yes" {
                    for !strings.Contains(output.snapshot()[previous:], "┃ >") {
                        select {
                        case <-ctx.Done():
                            t.Fatal(output.snapshot())
                        case <-time.After(time.Millisecond):
                        }
                    }
                } else {
                    waitWizardText(t, ctx, output, "┃ "+l.Text(step.title))
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
            // Reuse the restored terminal for the root overview, then exit.
            ctx2, stop := context.WithTimeout(context.Background(), 5*time.Second)
            defer stop()
            before := len(output.snapshot())
            go func() { done <- execute(ctx2, []string{"-i", "--lang", tc.lang, "cloud"}, o) }()
            for !strings.Contains(output.snapshot()[before:], l.Text("cloud_hub_title")) {
                select {
                case <-ctx2.Done():
                    t.Fatal(output.snapshot())
                case <-time.After(time.Millisecond):
                }
            }
            exportCloudScreen(t, fmt.Sprintf("hub-%s-%d-%d", tc.lang, tc.cols, tc.rows), output.raw())
            _, _ = io.WriteString(master, "\x1b[B\x1b[B\x1b[B\x1b[B\r")
            select {
            case code := <-done:
                if code != 0 {
                    t.Fatal(code, output.snapshot())
                }
            case <-ctx2.Done():
                t.Fatal(output.snapshot())
            }
        })
    }
}

func (w *wizardTerminal) raw() string { w.mu.Lock(); defer w.mu.Unlock(); return w.text.String() }

type manyBrowseCloud struct{ *uxCloud }

func (p *manyBrowseCloud) Search(context.Context, cloud.Plan, int) (cloud.InventoryPage, error) {
    p.searches++
    page := cloud.InventoryPage{Examined: 100, Complete: false, HasMore: true, NextOffset: 100}
    for n := 0; n < 100; n++ {
        page.Offers = append(page.Offers, cloud.Offer{ID: fmt.Sprintf("host-%03d", n), Model: "RTX 4090", GPUCount: 1, Available: cloudPointer(true), CPUs: cloudPointer(16 + n), ComputeHourlyPrice: &cloud.Money{Amount: "2.00", Currency: "CNY"}})
    }
    return page, nil
}

func TestCloudHubRichBoundedBrowseAndPagedResult(t *testing.T) {
    o, base := uxFixture(t)
    p := &manyBrowseCloud{base}
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil }
    settings, _ := o.Config.Load()
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    _ = pty.Setsize(terminal, &pty.Winsize{Rows: 18, Cols: 80})
    drained := make(chan struct{})
    go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
    output := &wizardTerminal{File: terminal}
    locale, _ := i18n.New("zh-Hans", nil)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    t.Cleanup(func() { cancel(); terminal.Close(); master.Close(); <-drained })
    call := &invocation{ui: true, app: client.New(o.Dependencies, settings), options: Options{In: terminal, Err: output}, output: &presenter{locale: locale, out: io.Discard, err: output}, grammar: &grammar{}, input: interaction.New(interaction.Options{In: terminal, Err: output, Rich: true, Interactive: true, Compact: true, PageHelp: locale.Text("cloud_ui_form_help"), SelectHelp: locale.Text("cloud_select_help")})}
    h := &cloudHub{call: call, selection: client.CloudSelect{Account: cloudPointer("main"), Product: cloudPointer("standard")}}
    waitFresh := func(start int, text string) {
        t.Helper()
        for !strings.Contains(output.snapshot()[start:], text) {
            select {
            case <-ctx.Done():
                t.Fatal(text, output.snapshot())
            case <-time.After(time.Millisecond):
            }
        }
    }
    done := make(chan error, 1)
    go func() { done <- h.search(ctx, client.CloudSearchInput{Select: h.selection, Limit: 100}) }()
    waitFresh(0, "┃ "+locale.Text("cloud_wizard_host"))
    exportCloudScreen(t, "browse-zh-Hans-80-18", output.raw())
    _, _ = io.WriteString(master, "\r")
    waitFresh(0, "┃ "+locale.Text("cloud_browse_actions"))
    start := len(output.snapshot())
    _, _ = io.WriteString(master, "\x1b[A\x1b[A\r") // Back -> More -> Sort.
    waitFresh(start, "┃ › "+locale.Text("cloud_candidates_sort_price"))
    start = len(output.snapshot())
    _, _ = io.WriteString(master, "\x1b[B\x1b[B\r")
    waitFresh(start, "┃ "+locale.Text("cloud_wizard_host"))
    start = len(output.snapshot())
    _, _ = io.WriteString(master, "\r")
    waitFresh(start, "┃ "+locale.Text("cloud_browse_actions"))
    _, _ = io.WriteString(master, "\r")
    if err := <-done; err != nil || p.creates != 0 || p.searches != 1 {
        t.Fatal(err, p.creates, p.searches)
    }
    catalog := cloud.CatalogPage{Account: "main", Product: "standard", Category: "image", Complete: true, Examined: 40}
    for n := 0; n < 40; n++ {
        catalog.Items = append(catalog.Items, cloud.CatalogItem{ID: fmt.Sprintf("image-%02d", n), Name: fmt.Sprintf("PyTorch version %02d", n)})
    }
    start = len(output.snapshot())
    go func() { done <- h.show(ctx, catalog, nil) }()
    waitFresh(start, "第 1 /")
    start = len(output.snapshot())
    _, _ = io.WriteString(master, "\x1b[A\r")
    waitFresh(start, "第 2 /")
    exportCloudScreen(t, "result-zh-Hans-80-18", output.raw())
    _, _ = io.WriteString(master, "\r")
    if err := <-done; err != nil {
        t.Fatal(err)
    }
}
