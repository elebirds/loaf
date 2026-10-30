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

    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "golang.org/x/term"
)

// Capture plain prompts as well as renderer frames; os.File's promoted
// WriteString would otherwise bypass wizardTerminal.Write.
type uxETerminal struct{ *wizardTerminal }

func (w *uxETerminal) WriteString(value string) (int, error) { return w.Write([]byte(value)) }

func uxECapture(t *testing.T, output *wizardTerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_E_CAPTURE_DIR"); directory != "" {
        if err := os.MkdirAll(directory, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(directory, name+".ansi"), []byte(output.raw()), 0600); err != nil {
            t.Fatal(err)
        }
    }
}

func TestUXERichMenuNativeStreamAndTerminalRestoration(t *testing.T) {
    for _, entry := range []struct {
        language      string
        columns, rows uint16
        light         bool
    }{{"en", 80, 24, false}, {"zh-Hans", 80, 18, false}, {"en", 120, 32, false}, {"zh-Hans", 80, 18, true}} {
        name := fmt.Sprintf("%s-%dx%d", entry.language, entry.columns, entry.rows)
        if entry.light {
            name = "light-" + name
        }
        t.Run(name, func(t *testing.T) {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            drained := make(chan struct{})
            go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: entry.rows, Cols: entry.columns}); err != nil {
                t.Fatal(err)
            }
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            output := &wizardTerminal{File: terminal}
            c, remote, _, id := uxEFixture(t, "", entry.language)
            c.options.In, c.options.Err = terminal, output
            c.output.err = output
            // Hide os.File's promoted WriteString so every raw log write also
            // passes through the fixture's capture writer.
            c.output.out = struct{ io.Writer }{output}
            l := c.output.locale
            c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help")})
            remote.entered, remote.release = make(chan struct{}), make(chan struct{})
            ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() { defer close(joined); _, err := c.uxELogs(ctx, id); done <- err }()
            t.Cleanup(func() { cancel(); <-joined })
            uxCoreWaitAfter(t, ctx, output, 0, l.Text("ux_core_select_help"))
            if entry.light {
                if _, err := io.WriteString(master, "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"); err != nil {
                    t.Fatal(err)
                }
                for c.input.Dark() {
                    select {
                    case <-ctx.Done():
                        t.Fatal("background reply was not consumed")
                    case <-time.After(time.Millisecond):
                    }
                }
                // A completed following form uses the recorded light palette.
                // Capture after its input transition rather than an arbitrary wait.
            }
            uxECapture(t, output, "menu-"+name)
            start := len(output.raw())
            if _, err := io.WriteString(master, "\x1b[B\r"); err != nil {
                t.Fatal(err)
            }
            uxCoreWaitAfter(t, ctx, output, start, l.Text("ux_e_tail_help"))
            uxECapture(t, output, "tail-"+name)
            if _, err := io.WriteString(master, "\r"); err != nil {
                t.Fatal(err)
            }
            select {
            case <-remote.entered:
            case <-ctx.Done():
                t.Fatal("stream did not begin", output.snapshot())
            }
            native, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, native) {
                t.Fatal("native stream retained raw UI mode", err)
            }
            // A possible native authentication read receives this input rather
            // than a leftover Tea input worker. The output consumer is drained.
            nativeInput := make(chan string, 1)
            go func() {
                var data [4]byte
                n, _ := io.ReadFull(terminal, data[:])
                nativeInput <- string(data[:n])
            }()
            if _, err := io.WriteString(master, "yes\n"); err != nil {
                t.Fatal(err)
            }
            select {
            case value := <-nativeInput:
                if value != "yes\n" {
                    t.Fatal(value)
                }
            case <-ctx.Done():
                t.Fatal("renderer stole native input")
            }
            close(remote.release)
            select {
            case err := <-done:
                if err != nil {
                    t.Fatal(err)
                }
            case <-ctx.Done():
                t.Fatal("stream did not return")
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) {
                t.Fatal("terminal attributes not restored", err)
            }
            if strings.Count(output.snapshot(), l.Text("ux_e_follow_done")) != 1 || strings.Count(output.snapshot(), "first 中文") != 1 || remote.controls != 0 {
                t.Fatal(output.snapshot(), remote.controls)
            }
            uxECapture(t, output, "finished-"+name)
        })
    }
}

func TestUXERichCancelDoesNotStartAnObservation(t *testing.T) {
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
    c, remote, _, id := uxEFixture(t, "", "zh-Hans")
    output := &wizardTerminal{File: terminal}
    c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: c.output.locale.Text("ux_core_form_help"), SelectHelp: c.output.locale.Text("ux_core_select_help")})
    ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
    done, joined := make(chan error, 1), make(chan struct{})
    go func() { defer close(joined); _, err := c.uxELogs(ctx, id); done <- err }()
    t.Cleanup(func() { cancel(); <-joined })
    uxCoreWaitAfter(t, ctx, output, 0, c.output.locale.Text("ux_core_select_help"))
    if _, err := io.WriteString(master, "\x03"); err != nil {
        t.Fatal(err)
    }
    select {
    case err := <-done:
        if err != interaction.ErrCanceled {
            t.Fatal(err)
        }
    case <-ctx.Done():
        t.Fatal("cancel did not return")
    }
    after, err := term.GetState(int(terminal.Fd()))
    if err != nil || !reflect.DeepEqual(before, after) || len(remote.requests) != 0 {
        t.Fatal(err, remote.requests)
    }
}

func TestUXERichDisconnectHandsInputToPlainRecovery(t *testing.T) {
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
    c, remote, _, id := uxEFixture(t, "", "zh-Hans")
    remote.mode = "disconnect"
    output := &wizardTerminal{File: terminal}
    captured := &uxETerminal{output}
    c.output.err, c.output.out = captured, captured
    l := c.output.locale
    c.input = interaction.New(interaction.Options{In: terminal, Err: captured, Interactive: true, Rich: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help")})
    ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
    done, joined := make(chan error, 1), make(chan struct{})
    go func() { defer close(joined); _, err := c.uxELogs(ctx, id); done <- err }()
    t.Cleanup(func() { cancel(); <-joined })
    uxCoreWaitAfter(t, ctx, output, 0, l.Text("ux_core_select_help"))
    start := len(output.raw())
    if _, err := io.WriteString(master, "\x1b[B\r"); err != nil {
        t.Fatal(err)
    }
    uxCoreWaitAfter(t, ctx, output, start, l.Text("ux_e_tail_help"))
    if _, err := io.WriteString(master, "\r"); err != nil {
        t.Fatal(err)
    }
    uxCoreWaitAfter(t, ctx, output, start, l.Text("ux_e_recovery_choice"))
    state, err := term.GetState(int(terminal.Fd()))
    if err != nil || !reflect.DeepEqual(before, state) || strings.Contains(output.snapshot(), "--resume") {
        t.Fatal("recovery did not use the plain input handoff", err, output.snapshot())
    }
    uxECapture(t, output, "recovery-zh-Hans-80x18")
    if _, err := io.WriteString(master, "2\n"); err != nil {
        t.Fatal(err)
    }
    select {
    case err := <-done:
        if err != nil {
            t.Fatal(err)
        }
    case <-ctx.Done():
        t.Fatal("explicit resume did not finish")
    }
    if len(remote.requests) != 2 || remote.requests[1].Resume == "" || remote.controls != 0 || strings.Count(output.snapshot(), "first 中文") != 1 || strings.Count(output.snapshot(), "second") != 1 {
        t.Fatal(remote.requests, output.snapshot())
    }
    after, err := term.GetState(int(terminal.Fd()))
    if err != nil || !reflect.DeepEqual(before, after) {
        t.Fatal("recovery did not restore echo", err)
    }
    uxECapture(t, output, "resumed-zh-Hans-80x18")
}
