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
    "golang.org/x/term"
)

type uxGTerminal struct{ *wizardTerminal }

func (w *uxGTerminal) WriteString(value string) (int, error) { return w.Write([]byte(value)) }

func uxGWaitAfter(t *testing.T, ctx context.Context, output *uxGTerminal, offset int, value string) {
    t.Helper()
    for {
        output.mu.Lock()
        text := output.text.String()[offset:]
        output.mu.Unlock()
        if !strings.HasPrefix(value, "\x1b[") {
            text = ansi.Strip(text)
        }
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

func uxGCapture(t *testing.T, output *uxGTerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_G_CAPTURE_DIR"); directory != "" {
        output.mu.Lock()
        data := append([]byte(nil), output.text.Bytes()...)
        output.mu.Unlock()
        if err := os.MkdirAll(directory, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(directory, name+".ansi"), data, 0600); err != nil {
            t.Fatal(err)
        }
    }
}

func TestUXGTerminalPreviewConfirmationAndRestore(t *testing.T) {
    for _, test := range []struct {
        language, mode string
        columns, rows  uint16
        accept         bool
    }{
        {"en", "dark", 80, 18, true}, {"zh-Hans", "light", 80, 18, false},
        {"en", "light", 80, 24, false}, {"zh-Hans", "dark", 80, 24, true},
        {"en", "dark", 120, 32, true}, {"zh-Hans", "light", 120, 32, false},
        {"en", "NO_COLOR", 80, 18, false}, {"zh-Hans", "TERM-dumb", 80, 18, false},
    } {
        name := fmt.Sprintf("%s-%s-%dx%d", test.language, test.mode, test.columns, test.rows)
        t.Run(name, func(t *testing.T) {
            t.Setenv("TERM", "xterm-256color")
            t.Setenv("COLORTERM", "truecolor")
            t.Setenv("NO_COLOR", "")
            if test.mode == "NO_COLOR" {
                t.Setenv("NO_COLOR", "1")
            }
            if test.mode == "TERM-dumb" {
                t.Setenv("TERM", "dumb")
            }
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            drained := make(chan struct{})
            go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: test.rows, Cols: test.columns}); err != nil {
                t.Fatal(err)
            }
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            output := &uxGTerminal{&wizardTerminal{File: terminal}}
            c, cleaner, ids := uxGMenuFixture(t, test.language, "", 1)
            c.options.In, c.options.Err, c.output.err = terminal, output, output
            rich := test.mode != "NO_COLOR" && test.mode != "TERM-dumb"
            l := c.output.locale
            c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: rich, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help")})
            ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() {
                defer close(joined)
                _, err := c.uxGClean(ctx, ids)
                done <- err
            }()
            t.Cleanup(func() { cancel(); <-joined })
            send := func(keys string) int {
                t.Helper()
                output.mu.Lock()
                offset := output.text.Len()
                output.mu.Unlock()
                if _, err := io.WriteString(master, keys); err != nil {
                    t.Fatal(err)
                }
                return offset
            }
            capture := func(stage string, offset int) {
                t.Helper()
                if rich {
                    // Each form asks the terminal again. Reply for every new
                    // renderer, just as a real terminal does, before capture.
                    response := "\x1b]11;rgb:1111/1111/1111\x1b\\"
                    accent := "\x1b[38;2;94;234;212m"
                    if test.mode == "light" {
                        response = "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"
                        accent = "\x1b[38;2;15;118;110m"
                    }
                    send(response)
                    uxGWaitAfter(t, ctx, output, offset, accent)
                }
                uxGCapture(t, output, stage+"-"+name)
            }
            uxGWaitAfter(t, ctx, output, 0, l.Text("ux_core_back"))
            capture("preview", 0)
            offset := send("\r")
            uxGWaitAfter(t, ctx, output, offset, l.Text("ux_g_cancel"))
            capture("actions", offset)
            keys := "1\n"
            if rich {
                keys = strings.Repeat("\x1b[A", 4) + "\r"
            }
            offset = send(keys)
            uxGWaitAfter(t, ctx, output, offset, l.Text("ux_g_confirm", map[string]any{"Count": 1}))
            if rich {
                uxGWaitAfter(t, ctx, output, offset, l.Text("ux_g_keep"))
            }
            capture("confirm", offset)
            keys = "\r"
            if test.accept {
                keys = "\x1b[D\r"
            }
            offset = send(keys)
            if test.accept {
                uxGWaitAfter(t, ctx, output, offset, l.Text("ux_g_result_title", map[string]any{"Count": 1}))
                capture("result", offset)
                send("\r")
            }
            select {
            case err := <-done:
                if err != nil {
                    t.Fatal(err, output.snapshot())
                }
            case <-ctx.Done():
                t.Fatal("cleanup did not finish", output.snapshot())
            }
            if (cleaner.cleans == 1) != test.accept || cleaner.previews != 1 || cleaner.repairs[0] {
                t.Fatal("confirmation or read-only boundary changed", cleaner)
            }
            if !rich {
                output.mu.Lock()
                escaped := strings.Contains(output.text.String(), "\x1b[")
                output.mu.Unlock()
                if escaped {
                    t.Fatal("plain cleanup emitted ANSI")
                }
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) {
                t.Fatal("cleanup did not restore the terminal", err)
            }
        })
    }
}

func TestUXGTerminalCancelNeverCleans(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("NO_COLOR", "")
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    drained := make(chan struct{})
    go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
    t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
    if err := pty.Setsize(terminal, &pty.Winsize{Rows: 18, Cols: 80}); err != nil {
        t.Fatal(err)
    }
    before, err := term.GetState(int(terminal.Fd()))
    if err != nil {
        t.Fatal(err)
    }
    c, cleaner, ids := uxGMenuFixture(t, "zh-Hans", "", 1)
    output := &uxGTerminal{&wizardTerminal{File: terminal}}
    c.options.In, c.options.Err, c.output.err = terminal, output, output
    c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true})
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    done, joined := make(chan error, 1), make(chan struct{})
    go func() { defer close(joined); _, err := c.uxGClean(ctx, ids); done <- err }()
    t.Cleanup(func() { cancel(); <-joined })
    uxGWaitAfter(t, ctx, output, 0, c.output.locale.Text("ux_core_back"))
    if _, err := io.WriteString(master, "\x03"); err != nil {
        t.Fatal(err)
    }
    select {
    case err := <-done:
        if err != interaction.ErrCanceled || cleaner.cleans != 0 {
            t.Fatal(err, cleaner)
        }
    case <-ctx.Done():
        t.Fatal("Ctrl+C did not end the preview")
    }
    after, err := term.GetState(int(terminal.Fd()))
    if err != nil || !reflect.DeepEqual(before, after) {
        t.Fatal("cancellation did not restore terminal", err)
    }
}
