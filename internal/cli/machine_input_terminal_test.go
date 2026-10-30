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
    "github.com/elebirds/loaf/internal/i18n"
    "golang.org/x/term"
)

func TestMachineAddInputsFinishWithoutNavigationAndRestoreTerminal(t *testing.T) {
    for _, tc := range []struct {
        lang          string
        columns, rows uint16
    }{{"en", 80, 18}, {"zh-Hans", 120, 24}} {
        t.Run(fmt.Sprintf("%s-%dx%d", tc.lang, tc.columns, tc.rows), func(t *testing.T) {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            drained := make(chan struct{})
            go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: tc.rows, Cols: tc.columns}); err != nil {
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
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            joined, done := make(chan struct{}), make(chan int, 1)
            go func() {
                defer close(joined)
                done <- execute(ctx, []string{"--lang", tc.lang, "machine", "add", "-i"}, o)
            }()
            t.Cleanup(func() { cancel(); terminal.Close(); master.Close(); <-drained; <-joined })
            l, _ := i18n.New(tc.lang, nil)
            waitWizardText(t, ctx, output, l.Text("ux_a_name"))
            if _, err := io.WriteString(master, "lab\r"); err != nil {
                t.Fatal(err)
            }
            for !strings.Contains(output.snapshot(), "┃ "+l.Text("ux_a_connection")) {
                select {
                case <-ctx.Done():
                    t.Fatal("connection field did not receive focus", output.snapshot())
                case <-time.After(time.Millisecond):
                }
            }
            if _, err := io.WriteString(master, "fixture-host\r"); err != nil {
                t.Fatal(err)
            }
            select {
            case code := <-done:
                if code != 0 {
                    t.Fatal(code, output.snapshot())
                }
            case <-ctx.Done():
                t.Fatal("input completion did not finish the command", output.snapshot())
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) {
                t.Fatal("command-local form did not restore terminal", err)
            }
            settings, err := o.Config.Load()
            if err != nil || settings.Targets["lab"].Destination != "fixture-host" {
                t.Fatal(settings, err, output.snapshot())
            }
            if directory := os.Getenv("LOAF_COMMAND_CAPTURE_DIR"); directory != "" {
                if err := os.MkdirAll(directory, 0700); err != nil {
                    t.Fatal(err)
                }
                name := filepath.Join(directory, fmt.Sprintf("machine-add-%s-%dx%d.ansi", tc.lang, tc.columns, tc.rows))
                if err := os.WriteFile(name, []byte(output.raw()), 0600); err != nil {
                    t.Fatal(err)
                }
            }
        })
    }
}
