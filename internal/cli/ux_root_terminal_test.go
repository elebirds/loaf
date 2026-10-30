//go:build !windows

package cli

import (
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/i18n"
    "golang.org/x/term"
)

func uxCoreWaitAfter(t *testing.T, ctx context.Context, output *wizardTerminal, offset int, value string) {
    t.Helper()
    for {
        output.mu.Lock()
        text := ansi.Strip(output.text.String()[offset:])
        output.mu.Unlock()
        if strings.Contains(text, value) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing new frame %q: %s", value, text)
        case <-time.After(time.Millisecond):
        }
    }
}

func uxCoreWaitFrame(t *testing.T, ctx context.Context, output *wizardTerminal, offset int, content, footer string) {
    t.Helper()
    for {
        output.mu.Lock()
        text := strings.ReplaceAll(ansi.Strip(output.text.String()[offset:]), "\r", "")
        output.mu.Unlock()
        start := strings.Index(text, content)
        if start >= 0 && strings.Contains(text[start+len(content):], footer) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing complete frame %q: %s", content, text)
        case <-time.After(time.Millisecond):
        }
    }
}

// Respond only to actual OSC background requests, as a terminal would. Keeping
// this on the PTY also exercises the native input handoff after each form.
func uxCoreDrainTerminal(master *os.File, light bool) <-chan struct{} {
    done := make(chan struct{})
    go func() {
        defer close(done)
        pending := ""
        data := make([]byte, 4096)
        for {
            n, err := master.Read(data)
            if err != nil {
                return
            }
            pending += string(data[:n])
            for strings.Contains(pending, "\x1b]11;?") {
                offset := strings.Index(pending, "\x1b]11;?")
                pending = pending[offset+len("\x1b]11;?"):]
                shade := "0000"
                if light {
                    shade = "ffff"
                }
                _, _ = io.WriteString(master, fmt.Sprintf("\x1b]11;rgb:%s/%s/%s\x1b\\", shade, shade, shade))
            }
            if len(pending) > 32 {
                pending = pending[len(pending)-32:]
            }
        }
    }()
    return done
}

func uxCoreWaitTheme(t *testing.T, ctx context.Context, output *wizardTerminal, offset int, light bool) {
    t.Helper()
    color := "38;2;94;234;212"
    if light {
        color = "38;2;15;118;110"
    }
    for {
        output.mu.Lock()
        ready := strings.Contains(output.text.String()[offset:], color)
        output.mu.Unlock()
        if ready {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatal("root theme response was not rendered")
        case <-time.After(time.Millisecond):
        }
    }
}

func TestUXCoreRichDetailPaginationAndRootCancel(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("COLORTERM", "truecolor")
    t.Setenv("NO_COLOR", "")
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    drained := uxCoreDrainTerminal(master, false)
    t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
    if err := pty.Setsize(terminal, &pty.Winsize{Rows: 18, Cols: 80}); err != nil {
        t.Fatal(err)
    }
    before, err := term.GetState(int(terminal.Fd()))
    if err != nil {
        t.Fatal(err)
    }
    output := &wizardTerminal{File: terminal}
    c := uxCoreCall(t, "")
    c.options.In, c.options.Err = terminal, output
    c.output.locale, _ = i18n.New("zh-Hans", nil)
    c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: c.output.locale.Text("ux_core_form_help"), SelectHelp: c.output.locale.Text("ux_core_select_help")})
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    done, joined := make(chan error, 1), make(chan struct{})
    go func() {
        defer close(joined)
        done <- c.uxShowDocument(ctx, interaction.Document{Title: "任务详情", Body: strings.Repeat("检查范围保留；恢复同一个任务。\n", 45)})
    }()
    t.Cleanup(func() { cancel(); <-joined })
    uxCoreWaitAfter(t, ctx, output, 0, "第 1 /")
    uxCoreCapture(t, output, "detail-zh-Hans-80x18")
    output.mu.Lock()
    start := output.text.Len()
    output.mu.Unlock()
    if _, err := io.WriteString(master, "\x1b[A\r"); err != nil {
        t.Fatal(err)
    }
    uxCoreWaitAfter(t, ctx, output, start, "第 2 /")
    uxCoreCapture(t, output, "detail-next-zh-Hans-80x18")
    if _, err := io.WriteString(master, "\r"); err != nil {
        t.Fatal(err)
    }
    if err := <-done; err != nil {
        t.Fatal(err)
    }
    o := fixture(t)
    o.In, o.Err = terminal, output
    o.Interactive, o.TerminalError = true, true
    o.Env["TERM"] = "xterm-256color"
    output.mu.Lock()
    start = output.text.Len()
    output.mu.Unlock()
    rootDone, rootJoined := make(chan int, 1), make(chan struct{})
    go func() { defer close(rootJoined); rootDone <- execute(ctx, []string{"-i"}, o) }()
    t.Cleanup(func() { cancel(); <-rootJoined })
    uxCoreWaitAfter(t, ctx, output, start, "Loaf")
    if _, err := io.WriteString(master, "\x03"); err != nil {
        t.Fatal(err)
    }
    if code := <-rootDone; code != 0 {
        t.Fatal("root cancellation was not graceful", code)
    }
    after, err := term.GetState(int(terminal.Fd()))
    if err != nil || !reflect.DeepEqual(before, after) {
        t.Fatal("details/root cancel did not restore terminal", err)
    }
}

func TestUXCoreRichRootCloudAndTerminalRestoration(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("COLORTERM", "truecolor")
    t.Setenv("NO_COLOR", "")
    for _, entry := range []struct {
        language      string
        rows, columns uint16
        light         bool
    }{{"en", 18, 80, false}, {"zh-Hans", 18, 80, false}, {"en", 32, 120, false}, {"zh-Hans", 24, 120, true}, {"en", 24, 80, true}} {
        name := entry.language
        if entry.light {
            name += "-light"
        }
        name = fmt.Sprintf("%s-%dx%d", name, entry.columns, entry.rows)
        t.Run(name, func(t *testing.T) {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            drained := uxCoreDrainTerminal(master, entry.light)
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: entry.rows, Cols: entry.columns}); err != nil {
                t.Fatal(err)
            }
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            output := &wizardTerminal{File: terminal}
            o := fixture(t)
            o.In, o.Err = terminal, output
            o.Interactive, o.TerminalError = true, true
            o.Env["TERM"] = "xterm-256color"
            ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
            done, joined := make(chan int, 1), make(chan struct{})
            go func() { defer close(joined); done <- execute(ctx, []string{"-i", "--lang", entry.language}, o) }()
            t.Cleanup(func() { cancel(); <-joined })
            l, _ := i18n.New(entry.language, nil)
            uxCoreWaitFrame(t, ctx, output, 0, "Loaf", l.Text("ux_core_select_help"))
            uxCoreWaitTheme(t, ctx, output, 0, entry.light)
            uxCoreCapture(t, output, "root-"+name)
            output.mu.Lock()
            start := output.text.Len()
            output.mu.Unlock()
            if _, err := io.WriteString(master, strings.Repeat("\x1b[B", uxCoreRootChoice(t, o, "cloud")-1)+"\r"); err != nil {
                t.Fatal(err)
            }
            uxCoreWaitFrame(t, ctx, output, start, l.Text("cloud_hub_no_account"), l.Text("ux_core_select_help"))
            uxCoreWaitTheme(t, ctx, output, start, entry.light)
            uxCoreCapture(t, output, "cloud-"+name)
            output.mu.Lock()
            start = output.text.Len()
            output.mu.Unlock()
            if _, err := io.WriteString(master, strings.Repeat("\x1b[B", 4)+"\r"); err != nil {
                t.Fatal(err)
            }
            uxCoreWaitFrame(t, ctx, output, start, l.Text("ux_core_run"), l.Text("ux_core_select_help"))
            if _, err := io.WriteString(master, strings.Repeat("\x1b[B", uxCoreRootChoice(t, o, "exit")-1)+"\r"); err != nil {
                t.Fatal(err)
            }
            select {
            case code := <-done:
                if code != 0 {
                    t.Fatal(code, output.snapshot())
                }
            case <-ctx.Done():
                t.Fatal("root did not return", output.snapshot())
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) {
                t.Fatal("root/child did not restore terminal", err)
            }
        })
    }
}

func uxCoreCapture(t *testing.T, output *wizardTerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_CORE_CAPTURE_DIR"); directory != "" {
        output.mu.Lock()
        data := append([]byte{}, output.text.Bytes()...)
        output.mu.Unlock()
        if err := os.MkdirAll(directory, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(directory, name+".ansi"), data, 0600); err != nil {
            t.Fatal(err)
        }
    }
}
