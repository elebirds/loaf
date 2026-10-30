//go:build !windows

package cli

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "golang.org/x/term"
)

// Capture WriteString as well as Write; os.File's promoted method otherwise
// bypasses the plain-menu transcript while still reaching the real terminal.
type uxATerminal struct {
    *wizardTerminal
    screen *uxAScreen
    cancel context.CancelFunc
}

type uxAScreen struct {
    mu   sync.Mutex
    text bytes.Buffer
}

func (s *uxAScreen) Write(value []byte) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.text.Write(value)
}

func (w *uxATerminal) raw() string {
    w.screen.mu.Lock()
    defer w.screen.mu.Unlock()
    return w.screen.text.String()
}

func (w *uxATerminal) snapshot() string { return ansi.Strip(w.raw()) }

func (w *uxATerminal) WriteString(value string) (int, error) { return w.Write([]byte(value)) }

func uxAOpenTerminal(t *testing.T, language string, columns, rows uint16, rich bool, names ...string) (*invocation, *os.File, *uxATerminal, context.Context) {
    t.Helper()
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("NO_COLOR", "")
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    screen := &uxAScreen{}
    drained := make(chan struct{})
    // Read the master side, including the terminal driver's newline mapping,
    // rather than mistaking bytes sent to the terminal for visible output.
    go func() { defer close(drained); _, _ = io.Copy(screen, master) }()
    t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
    if err := pty.Setsize(terminal, &pty.Winsize{Rows: rows, Cols: columns}); err != nil {
        t.Fatal(err)
    }
    output := &uxATerminal{wizardTerminal: &wizardTerminal{File: terminal}, screen: screen}
    c := uxATestCall(t, "", language, names...)
    c.options.In, c.options.Err, c.output.err = terminal, output, output
    c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: rich, Compact: true,
        PageHelp: c.output.locale.Text("ux_core_form_help"), SelectHelp: c.output.locale.Text("ux_core_select_help"), ChoiceError: c.output.locale.Text("ux_core_choice_invalid")})
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    output.cancel = cancel
    t.Cleanup(cancel)
    return c, master, output, ctx
}

func TestUXATerminalLightThemeAndLongAlias(t *testing.T) {
    name := strings.Repeat("long", 16)
    c, master, output, ctx := uxAOpenTerminal(t, "en", 80, 18, true, name)
    done, joined := make(chan error, 1), make(chan struct{})
    go func() { defer close(joined); _, err := c.uxAPickTarget(ctx, name); done <- err }()
    t.Cleanup(func() { output.cancel(); <-joined })
    uxAWaitTerminal(t, ctx, output, 0, c.output.locale.Text("ux_core_select_help"))
    start := uxAKeys(t, master, "\x1b]11;rgb:ffff/ffff/ffff\x1b\\", output)
    for c.input.Dark() {
        select {
        case <-ctx.Done():
            t.Fatal("Huh did not consume the terminal's light background response")
        case <-time.After(time.Millisecond):
        }
    }
    // Move and restore focus to obtain an observable frame after the background
    // reply, even when the color-only redraw reuses unchanged terminal lines.
    start = uxAKeys(t, master, "\x1b[B", output)
    uxAWaitTerminal(t, ctx, output, start, "› "+c.output.locale.Text("ux_a_search"))
    start = uxAKeys(t, master, "\x1b[A", output)
    uxAWaitTerminal(t, ctx, output, start, "› · SSH · longlong")
    uxACaptureTerminal(t, output, "picker-light-long-en-80x18")
    uxAKeys(t, master, "\x03", output)
    if err := <-done; !errors.Is(err, interaction.ErrCanceled) {
        t.Fatal(err)
    }
}

func uxAWaitTerminal(t *testing.T, ctx context.Context, output *uxATerminal, offset int, text string) {
    t.Helper()
    for {
        snapshot := ansi.Strip(output.raw()[offset:])
        if strings.Contains(snapshot, text) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing terminal frame %q: %s", text, snapshot)
        case <-time.After(time.Millisecond):
        }
    }
}

func uxAKeys(t *testing.T, master *os.File, keys string, output *uxATerminal) int {
    t.Helper()
    start := len(output.raw())
    if _, err := io.WriteString(master, keys); err != nil {
        t.Fatal(err)
    }
    return start
}

func uxACaptureTerminal(t *testing.T, output *uxATerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_A_CAPTURE_DIR"); directory != "" {
        if err := os.MkdirAll(directory, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(directory, name+".ansi"), []byte(output.raw()), 0600); err != nil {
            t.Fatal(err)
        }
    }
}

func TestUXATerminalHubDetailsAndBack(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, size := range [][2]uint16{{80, 18}, {80, 24}, {120, 32}} {
            for _, rich := range []bool{false, true} {
                mode := "plain"
                if rich {
                    mode = "huh"
                }
                name := fmt.Sprintf("%s-%s-%dx%d", mode, language, size[0], size[1])
                t.Run(name, func(t *testing.T) {
                    c, master, output, ctx := uxAOpenTerminal(t, language, size[0], size[1], rich, "alpha", "beta")
                    if _, err := c.app.Machines.UseTarget(ctx, "alpha"); err != nil {
                        t.Fatal(err)
                    }
                    before, err := term.GetState(int(output.Fd()))
                    if err != nil {
                        t.Fatal(err)
                    }
                    done, joined := make(chan error, 1), make(chan struct{})
                    go func() { defer close(joined); _, err := c.uxAMachineHub(ctx); done <- err }()
                    t.Cleanup(func() { output.cancel(); <-joined })
                    footer := "> "
                    if rich {
                        footer = c.output.locale.Text("ux_core_select_help")
                    }
                    uxAWaitTerminal(t, ctx, output, 0, footer)
                    if rich {
                        start := uxAKeys(t, master, "\x1b]11;rgb:2929/2c2c/3333\x1b\\\x1b[A", output)
                        uxAWaitTerminal(t, ctx, output, start, "› "+c.output.locale.Text("ux_a_add"))
                        start = uxAKeys(t, master, "\x1b[B", output)
                        uxAWaitTerminal(t, ctx, output, start, "› "+c.output.locale.Text("ux_core_back"))
                    }
                    uxACaptureTerminal(t, output, "hub-"+name)
                    keys := "1\n"
                    if rich {
                        keys = strings.Repeat("\x1b[A", 5) + "\r"
                    }
                    start := uxAKeys(t, master, keys, output)
                    uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_a_replace"))
                    uxAWaitTerminal(t, ctx, output, start, footer)
                    uxACaptureTerminal(t, output, "actions-"+name)
                    keys = "1\n"
                    if rich {
                        keys = strings.Repeat("\x1b[A", 3) + "\r"
                    }
                    start = uxAKeys(t, master, keys, output)
                    uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_a_destination"))
                    uxAWaitTerminal(t, ctx, output, start, footer)
                    uxACaptureTerminal(t, output, "details-"+name)
                    keys = "\n"
                    if rich {
                        keys = "\r"
                    }
                    start = uxAKeys(t, master, keys, output)
                    uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_a_replace"))
                    uxAWaitTerminal(t, ctx, output, start, footer)
                    start = uxAKeys(t, master, keys, output)
                    uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_a_refresh"))
                    uxAWaitTerminal(t, ctx, output, start, footer)
                    keys = "6\n"
                    if rich {
                        keys = strings.Repeat("\x1b[B", 5) + "\r"
                    }
                    uxAKeys(t, master, keys, output)
                    if err := <-done; err != nil {
                        t.Fatal(err, output.snapshot())
                    }
                    after, err := term.GetState(int(output.Fd()))
                    if err != nil || !reflect.DeepEqual(before, after) {
                        t.Fatal("machine hub failed to restore the terminal", err)
                    }
                    if !rich && strings.Contains(output.raw(), "\x1b[") {
                        t.Fatal("plain menu emitted ANSI")
                    }
                })
            }
        }
    }
}

func TestUXATerminalAddAndPickerCancel(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        t.Run(language, func(t *testing.T) {
            c, master, output, ctx := uxAOpenTerminal(t, language, 80, 18, true)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() { defer close(joined); _, err := c.uxAMachineAdd(ctx); done <- err }()
            t.Cleanup(func() { output.cancel(); <-joined })
            uxAWaitTerminal(t, ctx, output, 0, c.output.locale.Text("ux_core_form_help"))
            uxACaptureTerminal(t, output, "add-"+language+"-80x18")
            start := uxAKeys(t, master, "lab\r", output)
            // Huh moves field focus asynchronously. The unfocused label is
            // already visible on entry; wait for the focused field instead.
            uxAWaitTerminal(t, ctx, output, start, "┃ "+c.output.locale.Text("ux_a_connection"))
            start = uxAKeys(t, master, "lab-host\x1b[Z", output)
            uxAWaitTerminal(t, ctx, output, start, "┃ "+c.output.locale.Text("ux_a_name"))
            start = uxAKeys(t, master, "\r", output)
            uxAWaitTerminal(t, ctx, output, start, "┃ "+c.output.locale.Text("ux_a_connection"))
            start = uxAKeys(t, master, "\r", output)
            uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_a_review"))
            uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_core_select_help"))
            uxACaptureTerminal(t, output, "review-"+language+"-80x18")
            start = uxAKeys(t, master, "\r", output)
            uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_a_save"))
            uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_core_select_help"))
            uxACaptureTerminal(t, output, "save-"+language+"-80x18")
            uxAKeys(t, master, strings.Repeat("\x1b[A", 4)+"\r", output)
            if err := <-done; err != nil {
                t.Fatal(err, output.snapshot())
            }
            if c.app.Machines.Settings.Targets["lab"].Destination != "lab-host" {
                t.Fatal("terminal add did not persist the edited connection", c.app.Machines.Settings.Targets, output.snapshot())
            }
            pickerDone, pickerJoined := make(chan error, 1), make(chan struct{})
            start = len(output.raw())
            go func() { defer close(pickerJoined); _, err := c.uxAPickTarget(ctx, ""); pickerDone <- err }()
            t.Cleanup(func() { output.cancel(); <-pickerJoined })
            uxAWaitTerminal(t, ctx, output, start, c.output.locale.Text("ux_core_select_help"))
            uxACaptureTerminal(t, output, "picker-"+language+"-80x18")
            uxAKeys(t, master, "\x03", output)
            if err := <-pickerDone; !errors.Is(err, interaction.ErrCanceled) {
                t.Fatal("picker cancellation lost its explicit result", err)
            }
            if _, err := os.Stat(c.app.Machines.State.Path); !os.IsNotExist(err) {
                t.Fatal("terminal registration or picker created a project choice", err)
            }
        })
    }
}

func TestUXAEntryPlainTerminalModes(t *testing.T) {
    for _, mode := range []string{"NO_COLOR", "TERM=dumb"} {
        t.Run(mode, func(t *testing.T) {
            c, master, output, ctx := uxAOpenTerminal(t, "zh-Hans", 80, 18, false)
            options := c.options
            options.App, options.TerminalError, options.TerminalOutput = c.app, true, true
            options.Env = map[string]string{"TERM": "xterm-256color"}
            if mode == "NO_COLOR" {
                options.Env["NO_COLOR"] = "1"
            } else {
                options.Env["TERM"] = "dumb"
            }
            done, joined := make(chan int, 1), make(chan struct{})
            go func() {
                defer close(joined)
                done <- Execute(ctx, []string{"-i", "--lang", "zh-Hans", "machine"}, options)
            }()
            t.Cleanup(func() { output.cancel(); <-joined })
            uxAWaitTerminal(t, ctx, output, 0, "> ")
            uxACaptureTerminal(t, output, "entry-"+mode+"-80x18")
            uxAKeys(t, master, "4\n", output)
            if code := <-done; code != 0 || strings.Contains(output.raw(), "\x1b[") {
                t.Fatal(code, output.raw())
            }
        })
    }
}
