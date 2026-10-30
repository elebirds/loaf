package cli

import (
    "bufio"
    "context"
    "errors"
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
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/ssh"
    "golang.org/x/term"
)

type uxBNativeOperations struct {
    fakeOperations
    exec    func(context.Context, io.Reader, io.Writer, io.Writer) (ssh.ExecResult, error)
    forward func(context.Context) error
}

func (f *uxBNativeOperations) Exec(ctx context.Context, target config.Target, input ssh.ExecInput, in io.Reader, out, stderr io.Writer) (ssh.ExecResult, error) {
    f.calls++
    f.target, f.input = target, input
    return f.exec(ctx, in, out, stderr)
}

func (f *uxBNativeOperations) Forward(ctx context.Context, target config.Target, local, remote int, _ bool, _ io.Writer) error {
    f.calls++
    f.target, f.local, f.remote = target, local, remote
    return f.forward(ctx)
}

func uxBTerminal(t *testing.T, language string, columns, rows uint16) (*invocation, *os.File, *os.File, *wizardTerminal) {
    t.Helper()
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    drained := make(chan struct{})
    go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
    t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
    if err := pty.Setsize(terminal, &pty.Winsize{Rows: rows, Cols: columns}); err != nil {
        t.Fatal(err)
    }
    output := &wizardTerminal{File: terminal}
    o, _ := operationFixture(t)
    c := uxBCall(t, o, language, "")
    c.options.In, c.options.Err, c.output.err = terminal, output, output
    c.options.Interactive, c.options.TerminalError = true, true
    c.options.Env["TERM"] = "xterm-256color"
    l := c.output.locale
    c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help"), MultiHelp: l.Text("ux_core_multi_help")})
    return c, master, terminal, output
}

func uxBWaitFrame(t *testing.T, ctx context.Context, output *wizardTerminal, offset int, value string) int {
    t.Helper()
    for {
        output.mu.Lock()
        text, size := ansi.Strip(output.text.String()[offset:]), output.text.Len()
        output.mu.Unlock()
        if strings.Contains(text, value) {
            return size
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing frame %q: %s", value, text)
        case <-time.After(time.Millisecond):
        }
    }
}

func uxBCapture(t *testing.T, output *wizardTerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_B_CAPTURE_DIR"); directory != "" {
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

func TestUXBRichExecHandsInputToNativeAndRestoresTerminal(t *testing.T) {
    for _, tc := range []struct {
        language      string
        columns, rows uint16
    }{{"en", 80, 18}, {"zh-Hans", 80, 18}, {"en", 80, 24}, {"zh-Hans", 120, 32}} {
        t.Run(fmt.Sprintf("%s-%dx%d", tc.language, tc.columns, tc.rows), func(t *testing.T) {
            c, master, terminal, output := uxBTerminal(t, tc.language, tc.columns, tc.rows)
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            operation := &uxBNativeOperations{}
            operation.exec = func(_ context.Context, in io.Reader, out, diagnostic io.Writer) (ssh.ExecResult, error) {
                state, err := term.GetState(int(terminal.Fd()))
                if err != nil || !reflect.DeepEqual(state, before) {
                    t.Error("form still owns terminal modes", err)
                }
                fmt.Fprintln(diagnostic, "NATIVE_INPUT>")
                value, err := bufio.NewReader(in).ReadString('\n')
                if err != nil {
                    return ssh.ExecResult{}, err
                }
                _, err = io.WriteString(out, value)
                return ssh.ExecResult{Status: "succeeded"}, err
            }
            c.app.Machines.Operations = operation
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() {
                defer close(joined)
                name := "demo"
                _, err := c.uxBConnection(ctx, "exec", &name)
                done <- err
            }()
            t.Cleanup(func() { cancel(); terminal.Close(); <-joined })
            offset := uxBWaitFrame(t, ctx, output, 0, c.output.locale.Text("ux_b_argv"))
            uxBCapture(t, output, fmt.Sprintf("exec-mode-%s-%dx%d", tc.language, tc.columns, tc.rows))
            io.WriteString(master, "\r")
            offset = uxBWaitFrame(t, ctx, output, offset, c.output.locale.Text("ux_core_form_help"))
            io.WriteString(master, "printf '%s' 'a b' '$HOME' ''\r")
            offset = uxBWaitFrame(t, ctx, output, offset, c.output.locale.Text("ux_b_exec_review"))
            uxBCapture(t, output, fmt.Sprintf("exec-review-%s-%dx%d", tc.language, tc.columns, tc.rows))
            io.WriteString(master, "\x1b[A\x1b[A\r")
            uxBWaitFrame(t, ctx, output, offset, "NATIVE_INPUT>")
            io.WriteString(master, "native-answer\n")
            select {
            case err := <-done:
                if err != nil {
                    t.Fatal(err)
                }
            case <-ctx.Done():
                t.Fatal("native input was not handed off", output.snapshot())
            }
            cancel()
            if operation.calls != 1 || !reflect.DeepEqual(operation.input.Args, []string{"printf", "%s", "a b", "$HOME", ""}) {
                t.Fatal(operation.calls, operation.input.Args)
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(after, before) {
                t.Fatal("terminal was not restored", err)
            }
        })
    }
}

func TestUXBRichForwardReviewAndCancelDoesNotReopenForms(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        t.Run(language, func(t *testing.T) {
            c, master, terminal, output := uxBTerminal(t, language, 80, 18)
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            entered := make(chan struct{})
            operation := &uxBNativeOperations{forward: func(ctx context.Context) error { close(entered); <-ctx.Done(); return nil }}
            c.app.Machines.Operations = operation
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() {
                defer close(joined)
                name := "demo"
                _, err := c.uxBConnection(ctx, "forward", &name)
                done <- err
            }()
            t.Cleanup(func() { cancel(); <-joined })
            offset := uxBWaitFrame(t, ctx, output, 0, c.output.locale.Text("ux_b_remote_port"))
            io.WriteString(master, "6006\r")
            offset = uxBWaitFrame(t, ctx, output, offset, "┃ "+c.output.locale.Text("ux_b_local_port"))
            uxBCapture(t, output, "forward-form-"+language+"-80x18")
            io.WriteString(master, "16006\r")
            offset = uxBWaitFrame(t, ctx, output, offset, uxBMapping(16006, 6006))
            uxBCapture(t, output, "forward-review-"+language+"-80x18")
            io.WriteString(master, "\x1b[A\x1b[A\r")
            select {
            case <-entered:
                cancel()
            case <-ctx.Done():
                t.Fatal("tunnel was not started", output.snapshot())
            }
            select {
            case err := <-done:
                if !errors.Is(err, context.Canceled) {
                    t.Fatal(err)
                }
            case <-time.After(time.Second):
                t.Fatal("cancel did not return")
            }
            if operation.calls != 1 || operation.local != 16006 || operation.remote != 6006 {
                t.Fatal(operation)
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(after, before) {
                t.Fatal("terminal not restored", err)
            }
            tail := ansi.Strip(output.raw()[offset:])
            if strings.Count(tail, c.output.locale.Text("ux_b_forward_review")) > 1 {
                t.Fatal("cancel reopened form", tail)
            }
        })
    }
}

func TestUXBRichCheckStartsWithPagedEvidence(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        t.Run(language, func(t *testing.T) {
            c, master, _, output := uxBTerminal(t, language, 80, 18)
            row := checkResult{Target: "fixture", Steps: map[string]string{"connect": "verified", "authentication": "verified", "platform": "verified", "runner": "not_checked"}, Report: ssh.Report{OS: "Linux", Arch: "aarch64", Remote: map[string]ssh.Tool{"python": {Path: "/fixture/" + strings.Repeat("long-directory/", 12) + "python", Version: "Python fixture"}}}}
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() {
                defer close(joined)
                done <- c.uxShowDocument(ctx, uxBCheckDocument(row, c.output.locale, false))
            }()
            t.Cleanup(func() { cancel(); <-joined })
            offset := uxBWaitFrame(t, ctx, output, 0, c.output.locale.Text("check_step_connect"))
            uxBCapture(t, output, "check-overview-"+language+"-80x18")
            io.WriteString(master, "\x1b[A\r")
            uxBWaitFrame(t, ctx, output, offset, c.output.locale.Text("ux_core_page_count", map[string]any{"Count": 2, "Total": 3}))
            uxBCapture(t, output, "check-resources-"+language+"-80x18")
            io.WriteString(master, "\x03")
            if err := <-done; !errors.Is(err, interaction.ErrCanceled) {
                t.Fatal(err)
            }
        })
    }
}
