package interaction

import (
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/creack/pty"
)

// A real terminal consumes renderer output. Without this reader a long page
// sequence can fill macOS's PTY buffer and stall the renderer, especially -race.
func drainTerminal(t *testing.T, master, terminal *os.File) {
    t.Helper()
    done := make(chan struct{})
    go func() { defer close(done); _, _ = io.Copy(io.Discard, master) }()
    t.Cleanup(func() { terminal.Close(); master.Close(); <-done })
}

func waitRendered(t *testing.T, ctx context.Context, out *renderedPrompt, value string) {
    t.Helper()
    for {
        out.mu.Lock()
        rendered := out.text.String()
        out.mu.Unlock()
        if strings.Contains(rendered, value) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing rendered %q: %s", value, rendered)
        case <-time.After(time.Millisecond):
        }
    }
}

func pressRendered(t *testing.T, ctx context.Context, out *renderedPrompt, master io.Writer, keys string) {
    t.Helper()
    out.mu.Lock()
    previous := out.text.Len()
    out.mu.Unlock()
    if _, err := io.WriteString(master, keys); err != nil {
        t.Fatal(err)
    }
    for {
        out.mu.Lock()
        size := out.text.Len()
        out.mu.Unlock()
        if size > previous {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatal("key did not produce a frame", keys)
        case <-time.After(time.Millisecond):
        }
    }
}

func TestCloudPageRichChoicesAndRestoration(t *testing.T) {
    for _, tc := range []struct {
        columns uint16
        chinese bool
    }{{80, false}, {120, false}, {80, true}, {120, true}} {
        columns := tc.columns
        t.Run(fmt.Sprintf("%d-chinese-%t", columns, tc.chinese), func(t *testing.T) {
            master, terminal := openTerminal(t)
            drainTerminal(t, master, terminal)
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: 24, Cols: columns}); err != nil {
                t.Fatal(err)
            }
            before := terminalState(t, terminal)
            output := &renderedPrompt{File: terminal, title: "Choose compute", ready: make(chan struct{})}
            s := New(Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: "Up/down · Enter · Ctrl+C", SelectHelp: "Up/down select, / search", MultiHelp: "Space selects; Enter continues"})
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            gpu := &Field{Title: "GPU model", Choices: []Choice{{"RTX 4090", "4090"}, {"RTX 4090D", "4090d"}}}
            regions := &Field{Title: "Allowed regions", Multiple: true, Choices: []Choice{{"West", "west"}, {"North", "north"}}}
            nav := &Field{Title: "Continue?", Choices: []Choice{{"Next", "next"}, {"Cancel", "cancel"}}}
            if tc.chinese {
                gpu.Title, regions.Title, nav.Title = "GPU 型号", "允许的地区", "下一步"
                s.options.SelectHelp, s.options.MultiHelp = "↑↓ 选择 · / 筛选 · Enter 继续", "空格勾选 · Enter 继续"
            }
            for _, entry := range []struct {
                field *Field
                keys  string
            }{{gpu, "\x1b[B\r"}, {regions, " \r"}, {nav, "\r"}} {
                done, joined := make(chan error, 1), make(chan struct{})
                go func() { defer close(joined); done <- s.Page(ctx, "main / standard · Choose compute", entry.field) }()
                // Join before the fixture closes its fd, including on failure.
                t.Cleanup(func() { cancel(); <-joined })
                waitRendered(t, ctx, output, entry.field.Title)
                if _, err := io.WriteString(master, entry.keys); err != nil {
                    t.Fatal(err)
                }
                if err := <-done; err != nil {
                    t.Fatal(err)
                }
            }
            if gpu.Value != "4090d" || len(regions.Values) != 1 || regions.Values[0] != "west" || nav.Value != "next" {
                t.Fatal(gpu, regions, nav)
            }
            if after := terminalState(t, terminal); *after != *before {
                t.Fatal("page did not restore terminal")
            }
        })
    }
}

func TestSelectionDetailSnapshotHasNoConcurrentFormReads(t *testing.T) {
    master, terminal := openTerminal(t)
    before := terminalState(t, terminal)
    output := &renderedPrompt{File: terminal, title: "Choose host", ready: make(chan struct{})}
    s := New(Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true})
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    field := &Field{Title: "Choose host", Choices: []Choice{{"Cheap", "cheap"}, {"Wanted", "wanted"}}, Detail: func(id string) string { return "Details for " + id }}
    done := make(chan error, 1)
    go func() { done <- s.Page(ctx, "Choose host", field) }()
    waitRendered(t, ctx, output, "Choose host")
    pressRendered(t, ctx, output, master, "\x1b[B")
    if _, err := io.WriteString(master, "\r"); err != nil {
        t.Fatal(err)
    }
    if err := <-done; err != nil || field.Value != "wanted" {
        t.Fatal(err, field)
    }
    if after := terminalState(t, terminal); *after != *before {
        t.Fatal("selection details did not restore terminal")
    }
}

func TestLiveProgressDetailsCancellationAndNextInput(t *testing.T) {
    master, terminal := openTerminal(t)
    before := terminalState(t, terminal)
    output := &renderedPrompt{File: terminal, title: "Waiting for RTX 4090", ready: make(chan struct{})}
    s := New(Options{In: terminal, Err: output, Interactive: true, Rich: true})
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    live, err := s.StartProgress(ctx, cancel, ProgressFrame{Document: Document{Title: "Waiting for RTX 4090", Body: "Polls 7 · attempts 0"}, Detail: "No stock: 12", StartedAt: time.Now(), NextAt: time.Now().Add(10 * time.Second), ElapsedLabel: "Elapsed", NextLabel: "Next check", DetailTitle: "Details", Footer: "Tab details · Ctrl+C cancel"})
    if err != nil || live == nil {
        t.Fatal(live, err)
    }
    waitRendered(t, ctx, output, "Waiting for RTX 4090")
    if _, err := io.WriteString(master, "\t"); err != nil {
        t.Fatal(err)
    }
    waitRendered(t, ctx, output, "No stock: 12")
    if _, err := io.WriteString(master, "\x03"); err != nil {
        t.Fatal(err)
    }
    <-ctx.Done()
    if err := live.Close(); err != nil {
        t.Fatal(err)
    }
    if after := terminalState(t, terminal); *after != *before {
        t.Fatal("progress did not restore terminal")
    }
    next := make(chan error, 1)
    go func() {
        value, err := s.Plain().Text(context.Background(), TextPrompt{Title: "Next input"})
        if value != "next" && err == nil {
            err = fmt.Errorf("input stolen: %q", value)
        }
        next <- err
    }()
    if _, err := io.WriteString(master, "next\n"); err != nil {
        t.Fatal(err)
    }
    select {
    case err := <-next:
        if err != nil {
            t.Fatal(err)
        }
    case <-time.After(3 * time.Second):
        t.Fatal("progress input worker remained active")
    }
}

func TestLiveProgressOutputFailureCancelsOperationAndRestoresTerminal(t *testing.T) {
    _, terminal := openTerminal(t)
    before := terminalState(t, terminal)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    s := New(Options{In: terminal, Err: failedTerminal{terminal}, Interactive: true, Rich: true})
    live, err := s.StartProgress(ctx, cancel, ProgressFrame{Document: Document{Title: "Waiting"}})
    if err != nil {
        t.Fatal(err)
    }
    <-ctx.Done()
    if !errors.Is(live.Close(), io.ErrClosedPipe) {
        t.Fatal("lost terminal output failure")
    }
    if after := terminalState(t, terminal); *after != *before {
        t.Fatal("failed progress did not restore terminal")
    }
}

func TestSummaryConfirmationRichTypedAndRestoration(t *testing.T) {
    for _, tc := range []struct {
        name, keys string
        yes        bool
        canceled   bool
    }{{"yes", "yes\r", true, false}, {"not-yes", "y\r", false, false}, {"cancel", "\x03", false, true}} {
        t.Run(tc.name, func(t *testing.T) {
            master, terminal := openTerminal(t)
            before := terminalState(t, terminal)
            output := &renderedPrompt{File: terminal, title: "Type yes", ready: make(chan struct{})}
            s := New(Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true})
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            summary := Document{Title: "Approve one instance", Context: "gpu · demo", Rows: []Row{{"GPU", "RTX 4090 × 1"}}, Body: "Compute cap ¥3/hour. Storage is billed separately."}
            type result struct {
                yes bool
                err error
            }
            done, joined := make(chan result, 1), make(chan struct{})
            go func() {
                defer close(joined)
                yes, err := s.Confirm(ctx, Confirmation{Title: "Type yes", Kind: ConfirmTypedYes, Summary: &summary})
                done <- result{yes, err}
            }()
            t.Cleanup(func() { cancel(); <-joined })
            waitRendered(t, ctx, output, "Type yes")
            if _, err := io.WriteString(master, tc.keys); err != nil {
                t.Fatal(err)
            }
            got := <-done
            if got.yes != tc.yes || tc.canceled != errors.Is(got.err, ErrCanceled) || !tc.canceled && got.err != nil {
                t.Fatal(got)
            }
            if after := terminalState(t, terminal); *after != *before {
                t.Fatal("confirmation did not restore terminal")
            }
        })
    }
}
