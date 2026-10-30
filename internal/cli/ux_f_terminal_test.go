package cli

import (
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "regexp"
    "strings"
    "testing"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/i18n"
    "golang.org/x/term"
)

type uxFTerminal struct{ *wizardTerminal }

func (w *uxFTerminal) WriteString(value string) (int, error) { return w.Write([]byte(value)) }
func uxFWait(t *testing.T, ctx context.Context, out *uxFTerminal, offset int, value string) {
    t.Helper()
    for {
        text := ansi.Strip(out.raw()[offset:])
        if strings.Contains(text, value) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing %q: %s", value, text)
        case <-time.After(time.Millisecond):
        }
    }
}

func uxFWaitFrame(t *testing.T, ctx context.Context, out *uxFTerminal, offset int, content, footer string) {
    t.Helper()
    for {
        text := ansi.Strip(out.raw()[offset:])
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
func uxFKeys(t *testing.T, master *os.File, out *uxFTerminal, keys string) int {
    t.Helper()
    offset := len(out.raw())
    if _, err := io.WriteString(master, keys); err != nil {
        t.Fatal(err)
    }
    return offset
}
func uxFCapture(t *testing.T, out *uxFTerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_F_CAPTURE_DIR"); directory != "" {
        if err := os.MkdirAll(directory, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(directory, name+".ansi"), []byte(out.raw()), 0600); err != nil {
            t.Fatal(err)
        }
    }
}

func TestUXFTerminalBrowseSelectReviewDownloadAndCancel(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, size := range []struct{ cols, rows uint16 }{{80, 18}, {80, 24}, {120, 32}} {
            name := fmt.Sprintf("%s-%dx%d", language, size.cols, size.rows)
            t.Run(name, func(t *testing.T) {
                t.Setenv("NO_COLOR", "")
                t.Setenv("TERM", "xterm-256color")
                master, terminal, err := pty.Open()
                if err != nil {
                    t.Fatal(err)
                }
                drained := make(chan struct{})
                go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
                t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
                if err := pty.Setsize(terminal, &pty.Winsize{Cols: size.cols, Rows: size.rows}); err != nil {
                    t.Fatal(err)
                }
                before, err := term.GetState(int(terminal.Fd()))
                if err != nil {
                    t.Fatal(err)
                }
                out := &uxFTerminal{&wizardTerminal{File: terminal}}
                c, b, p, refs := uxFCall(t, language, "")
                c.options.In, c.options.Err, c.output.err = terminal, out, out
                l := c.output.locale
                c.input = interaction.New(interaction.Options{In: terminal, Err: out, Interactive: true, Rich: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help"), MultiHelp: l.Text("ux_core_multi_help")})
                ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
                done := make(chan error, 1)
                joined := make(chan struct{})
                go func() { defer close(joined); _, err := c.uxFOutputs(ctx, refs[0].ID); done <- err }()
                t.Cleanup(func() { cancel(); <-joined })
                uxFWait(t, ctx, out, 0, l.Text("ux_core_select_help"))
                if size.rows == 24 {
                    uxFKeys(t, master, out, "\x1b]11;rgb:ffff/ffff/ffff\x07")
                    for c.input.Dark() {
                        select {
                        case <-ctx.Done():
                            t.Fatal("light theme response not consumed")
                        case <-time.After(time.Millisecond):
                        }
                    }
                }
                uxFCapture(t, out, "browse-"+name)
                offset := uxFKeys(t, master, out, "\x1b[B\r")
                uxFWait(t, ctx, out, offset, l.Text("ux_core_multi_help"))
                uxFCapture(t, out, "paths-"+name)
                offset = uxFKeys(t, master, out, "\x1b[B \r")
                uxFWait(t, ctx, out, offset, l.Text("ux_f_use_selection"))
                offset = uxFKeys(t, master, out, "\r")
                uxFWait(t, ctx, out, offset, l.Text("ux_f_outputs", map[string]any{"ID": refs[0].ID}))
                offset = uxFKeys(t, master, out, "\x1b[B\x1b[B\r")
                uxFWait(t, ctx, out, offset, l.Text("ux_f_details"))
                uxFCapture(t, out, "review-"+name)
                offset = uxFKeys(t, master, out, "\r")
                uxFWaitFrame(t, ctx, out, offset, refs[0].ID+" · \"lab\"", l.Text("ux_core_select_help"))
                uxFCapture(t, out, "review-details-"+name)
                offset = uxFKeys(t, master, out, "\r")
                uxFWait(t, ctx, out, offset, l.Text("ux_f_download"))
                offset = uxFKeys(t, master, out, "\x1b[B\r")
                uxFWait(t, ctx, out, offset, l.Text("ux_f_result_details"))
                uxFCapture(t, out, "result-"+name)
                down := 1
                if c.uxRouteAvailable("clean") {
                    down++
                }
                offset = uxFKeys(t, master, out, strings.Repeat("\x1b[B", down)+"\r")
                uxFWait(t, ctx, out, offset, l.Text("ux_f_outputs", map[string]any{"ID": refs[0].ID}))
                uxFKeys(t, master, out, "\x03")
                if err := <-done; !errors.Is(err, interaction.ErrCanceled) {
                    t.Fatal(err)
                }
                after, err := term.GetState(int(terminal.Fd()))
                if err != nil || !reflect.DeepEqual(before, after) {
                    t.Fatal("terminal not restored", err)
                }
                if len(b.calls) != 1 || p.calls != 1 || len(p.paths) != 1 || p.paths[0] != "outputs/中文 space[1]*?,.csv" {
                    t.Fatal(b, p)
                }
            })
        }
    }
}

func TestUXFPlainNoColorAndDumbRootToResults(t *testing.T) {
    for _, mode := range []string{"NO_COLOR", "TERM=dumb"} {
        t.Run(mode, func(t *testing.T) {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            drained := make(chan struct{})
            go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err := pty.Setsize(terminal, &pty.Winsize{Cols: 80, Rows: 18}); err != nil {
                t.Fatal(err)
            }
            out := &uxFTerminal{&wizardTerminal{File: terminal}}
            o, _, refs := psFixture(t)
            browser, puller := &uxFBrowser{}, &fakePuller{}
            o.Outputs, o.Puller = browser, puller
            o.In, o.Err, o.Interactive, o.TerminalError = terminal, out, true, true
            if mode == "NO_COLOR" {
                o.Env["NO_COLOR"] = "1"
                o.Env["TERM"] = "xterm-256color"
            } else {
                o.Env["TERM"] = "dumb"
            }
            l, _ := i18n.New("zh-Hans", nil)
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            done := make(chan int, 1)
            joined := make(chan struct{})
            go func() { defer close(joined); done <- execute(ctx, []string{"-i", "--lang", "zh-Hans"}, o) }()
            t.Cleanup(func() { cancel(); <-joined })
            offset := uxFPlainChoose(t, ctx, master, out, 0, "Loaf", l.Text("ux_core_results"))
            uxFWaitFrame(t, ctx, out, offset, l.Text("ux_d_pick"), "> ")
            offset = uxFKeys(t, master, out, "1\n")
            offset = uxFPlainChoose(t, ctx, master, out, offset, l.Text("ux_d_pick"), l.Text("ux_d_use_selection", map[string]any{"Count": 1}))
            offset = uxFPlainChoose(t, ctx, master, out, offset, l.Text("ux_f_outputs", map[string]any{"ID": refs[0].ID}), l.Text("ux_f_back"))
            uxFPlainChoose(t, ctx, master, out, offset, "Loaf", l.Text("ux_core_exit"))
            if code := <-done; code != 0 {
                t.Fatal(code, out.snapshot())
            }
            if len(browser.calls) != 1 || browser.calls[0].ref.ID != refs[0].ID || puller.calls != 0 || strings.Contains(out.raw(), "\x1b[") {
                t.Fatal(browser, puller, out.raw())
            }
            uxFCapture(t, out, "root-"+strings.ReplaceAll(mode, "=", "-")+"-zh-Hans-80x18")
        })
    }
}

// Read the actual menu label rather than duplicating another owner's root
// option inventory, which grows as domains are integrated.
func uxFPlainChoose(t *testing.T, ctx context.Context, master *os.File, out *uxFTerminal, offset int, title, label string) int {
    t.Helper()
    pattern := regexp.MustCompile(`(?m)^[ *]+([0-9]+)\. ` + regexp.QuoteMeta(label) + `\r?$`)
    for {
        text := out.raw()[offset:]
        start := strings.LastIndex(text, title)
        if start >= 0 {
            page := text[start:]
            match := pattern.FindStringSubmatchIndex(page)
            if match != nil && strings.Contains(page[match[1]:], "> ") {
                return uxFKeys(t, master, out, page[match[2]:match[3]]+"\n")
            }
        }
        select {
        case <-ctx.Done():
            t.Fatalf("menu option %q not available on %q: %s", label, title, text)
        case <-time.After(time.Millisecond):
        }
    }
}
