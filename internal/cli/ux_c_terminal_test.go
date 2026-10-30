//go:build !windows

package cli

import (
    "bufio"
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
    execution "github.com/elebirds/loaf/internal/run"
    "golang.org/x/term"
)

type uxCTerminal struct {
    *wizardTerminal
    screen *uxCScreen
}

type uxCScreen struct {
    mu   sync.Mutex
    data bytes.Buffer
}

func (s *uxCScreen) Write(p []byte) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.data.Write(p)
}

func (s *uxCScreen) snapshot() string {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.data.String()
}

func (w *uxCTerminal) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func uxCWaitFrame(t *testing.T, ctx context.Context, output *uxCTerminal, offset int, text string) {
    t.Helper()
    for {
        current := ansi.Strip(output.screen.snapshot()[offset:])
        if strings.Contains(current, text) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing frame %q: %s", text, current)
        case <-time.After(time.Millisecond):
        }
    }
}
func uxCFrameOffset(output *uxCTerminal) int {
    return len(output.screen.snapshot())
}
func uxCCapture(t *testing.T, output *uxCTerminal, name string) {
    t.Helper()
    directory := os.Getenv("LOAF_UX_C_CAPTURE_DIR")
    if directory == "" {
        return
    }
    if err := os.MkdirAll(directory, 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(directory, name+".ansi"), []byte(output.screen.snapshot()), 0600); err != nil {
        t.Fatal(err)
    }
}
func uxCOpenTerminal(t *testing.T, rows, cols uint16) (*os.File, *os.File, *uxCTerminal) {
    t.Helper()
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    drained := make(chan struct{})
    screen := &uxCScreen{}
    go func() { defer close(drained); _, _ = io.Copy(screen, master) }()
    t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
    if err := pty.Setsize(terminal, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
        t.Fatal(err)
    }
    return master, terminal, &uxCTerminal{wizardTerminal: &wizardTerminal{File: terminal}, screen: screen}
}
func uxCRichCall(c *invocation, terminal *os.File, output *uxCTerminal) {
    l := c.output.locale
    c.options.In, c.options.Err = terminal, output
    c.output.err = output
    c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true,
        PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help"), ChoiceError: l.Text("ux_core_choice_invalid")})
}

func TestUXCRichReviewAndSyncRestoreTerminal(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("COLORTERM", "truecolor")
    t.Setenv("NO_COLOR", "")
    for _, language := range []string{"en", "zh-Hans"} {
        for _, size := range [][2]uint16{{18, 80}, {24, 80}, {32, 120}} {
            light := language == "zh-Hans" && size[0] == 24
            name := fmt.Sprintf("%s-%dx%d", language, size[1], size[0])
            if light {
                name = fmt.Sprintf("%s-light-%dx%d", language, size[1], size[0])
            }
            t.Run(name, func(t *testing.T) {
                master, terminal, output := uxCOpenTerminal(t, size[0], size[1])
                before, err := term.GetState(int(terminal.Fd()))
                if err != nil {
                    t.Fatal(err)
                }
                o, checker, _, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='python3 train.py --seed 1'\nsetup='true'\n[run.env]\nPRIVATE='never-print-this'\n")
                c := uxCCall(t, o, language, "", "run", "--wait", "0s")
                uxCRichCall(c, terminal, output)
                ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
                defer cancel()
                done, joined := make(chan error, 1), make(chan struct{})
                go func() { defer close(joined); in, _ := c.uxCRunArgs(); _, err := c.uxCRunDraft(ctx, in); done <- err }()
                t.Cleanup(func() { cancel(); <-joined })
                uxCWaitFrame(t, ctx, output, 0, c.output.locale.Text("ux_core_select_help"))
                offset := uxCFrameOffset(output)
                response := "\x1b]11;rgb:1111/1818/2727\x1b\\"
                if light {
                    response = "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"
                }
                if _, err := io.WriteString(master, response); err != nil {
                    t.Fatal(err)
                }
                uxCWaitFrame(t, ctx, output, offset, c.output.locale.Text("ux_c_run_review"))
                // A pending repaint may repeat the title before the OSC reply
                // has been processed. Observe the requested accent itself.
                accent := "38;2;94;234;212"
                if light {
                    accent = "38;2;15;118;110"
                }
                for !strings.Contains(output.screen.snapshot()[offset:], accent) {
                    select {
                    case <-ctx.Done():
                        t.Fatal("terminal did not render the requested background theme")
                    case <-time.After(time.Millisecond):
                    }
                }
                if c.input.Dark() == light {
                    t.Fatal("terminal background response was not consumed")
                }
                uxCCapture(t, output, "run-review-"+name)
                offset = uxCFrameOffset(output)
                if _, err := io.WriteString(master, "\r"); err != nil {
                    t.Fatal(err)
                }
                uxCWaitFrame(t, ctx, output, offset, c.output.locale.Text("ux_c_execution_details"))
                offset = uxCFrameOffset(output)
                if _, err := io.WriteString(master, "\r"); err != nil {
                    t.Fatal(err)
                }
                uxCWaitFrame(t, ctx, output, offset, c.output.locale.Text("run_preview_heading"))
                uxCCapture(t, output, "run-details-"+name)
                offset = uxCFrameOffset(output)
                if _, err := io.WriteString(master, "\r"); err != nil {
                    t.Fatal(err)
                }
                uxCWaitFrame(t, ctx, output, offset, c.output.locale.Text("ux_c_run_review"))
                if _, err := io.WriteString(master, "\x03"); err != nil {
                    t.Fatal(err)
                }
                if err := <-done; !errors.Is(err, interaction.ErrCanceled) {
                    t.Fatal(err)
                }
                if checker.calls != 0 || remote.launches != 0 || strings.Contains(output.raw(), "never-print-this") {
                    t.Fatal("review leaked or connected", output.snapshot())
                }
                after, err := term.GetState(int(terminal.Fd()))
                if err != nil || !reflect.DeepEqual(before, after) {
                    t.Fatal("run terminal not restored", err)
                }
                syncDone, syncJoined := make(chan error, 1), make(chan struct{})
                offset = uxCFrameOffset(output)
                go func() { defer close(syncJoined); _, err := c.uxCSync(ctx); syncDone <- err }()
                t.Cleanup(func() { cancel(); <-syncJoined })
                uxCWaitFrame(t, ctx, output, offset, c.output.locale.Text("ux_c_sync_review"))
                uxCCapture(t, output, "sync-review-"+name)
                if _, err := io.WriteString(master, "\x03"); err != nil {
                    t.Fatal(err)
                }
                if err := <-syncDone; !errors.Is(err, interaction.ErrCanceled) {
                    t.Fatal(err)
                }
                after, err = term.GetState(int(terminal.Fd()))
                if err != nil || !reflect.DeepEqual(before, after) {
                    t.Fatal("sync terminal not restored", err)
                }
                if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
                    t.Fatal("review created state", err)
                }
            })
        }
    }
}

func TestUXCRichMissingCommandNativeAuthenticationAndLogs(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("COLORTERM", "truecolor")
    t.Setenv("NO_COLOR", "")
    master, terminal, output := uxCOpenTerminal(t, 18, 80)
    before, err := term.GetState(int(terminal.Fd()))
    if err != nil {
        t.Fatal(err)
    }
    o, checker, _, remote := uxCFixture(t, "target='lab'")
    c := uxCCall(t, o, "en", "", "run", "--wait", "1s")
    uxCRichCall(c, terminal, output)
    c.output.out = output
    checker.native = func() error {
        if checker.calls > 1 {
            return nil
        }
        state, err := term.GetState(int(terminal.Fd()))
        if err != nil || !reflect.DeepEqual(before, state) {
            return fmt.Errorf("renderer still owns native input: %w", err)
        }
        if _, err := fmt.Fprint(output, "Fixture authentication: "); err != nil {
            return err
        }
        value, err := bufio.NewReader(terminal).ReadString('\n')
        if err != nil {
            return err
        }
        if strings.TrimSpace(value) != "fixture-ok" {
            return fmt.Errorf("unexpected fixture input")
        }
        return nil
    }
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    done, joined := make(chan error, 1), make(chan struct{})
    var data any
    go func() { defer close(joined); var err error; data, err = c.uxCRunCommand(ctx); done <- err }()
    t.Cleanup(func() { cancel(); <-joined })
    uxCWaitFrame(t, ctx, output, 0, "Explicit Shell script")
    if _, err := io.WriteString(master, "\r"); err != nil {
        t.Fatal(err)
    }
    uxCWaitFrame(t, ctx, output, 0, "Separate arguments")
    uxCCapture(t, output, "command-input-en-80x18")
    if _, err := io.WriteString(master, "printf 'hello world' '$HOME' ';' --\r"); err != nil {
        t.Fatal(err)
    }
    uxCWaitFrame(t, ctx, output, 0, "Review this run")
    if _, err := io.WriteString(master, "\x1b[A\r"); err != nil {
        t.Fatal(err)
    }
    uxCWaitFrame(t, ctx, output, 0, "Fixture authentication")
    uxCCapture(t, output, "native-auth-en-80x18")
    if _, err := io.WriteString(master, "fixture-ok\n"); err != nil {
        t.Fatal(err)
    }
    select {
    case err := <-done:
        if err != nil {
            t.Fatal(err, output.snapshot())
        }
    case <-ctx.Done():
        t.Fatal("run did not finish", output.snapshot())
    }
    view, ok := data.(execution.View)
    if !ok || view.ID == "" || view.Log != nil || remote.launches != 1 || !reflect.DeepEqual(remote.req.Args, []string{"printf", "hello world", "$HOME", ";", "--"}) || strings.Count(output.raw(), "中文 log") != 1 {
        t.Fatal(data, remote, output.snapshot())
    }
    printRun(output, view, c.output.locale)
    uxCWaitFrame(t, ctx, output, 0, "exit=7")
    uxCCapture(t, output, "run-log-en-80x18")
    editDone, editJoined := make(chan error, 1), make(chan struct{})
    offset := uxCFrameOffset(output)
    go func() { defer close(editJoined); _, err := c.uxCEditCommand(ctx, remote.req.Args); editDone <- err }()
    t.Cleanup(func() { cancel(); <-editJoined })
    uxCWaitFrame(t, ctx, output, offset, "Keep current argv")
    if _, err := io.WriteString(master, "\x03"); err != nil {
        t.Fatal(err)
    }
    if err := <-editDone; !errors.Is(err, interaction.ErrCanceled) {
        t.Fatal(err)
    }
    after, err := term.GetState(int(terminal.Fd()))
    if err != nil || !reflect.DeepEqual(before, after) {
        t.Fatal("native/log/edit terminal not restored", err)
    }
}
