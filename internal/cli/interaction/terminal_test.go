package interaction

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "sync"
    "testing"
    "time"

    "github.com/creack/pty"
    "golang.org/x/sys/unix"
)

func openTerminal(t *testing.T) (*os.File, *os.File) {
    t.Helper()
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { master.Close(); terminal.Close() })
    if err := pty.Setsize(terminal, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
        t.Fatal(err)
    }
    return master, terminal
}

func terminalState(t *testing.T, terminal *os.File) *unix.Termios {
    t.Helper()
    state, err := unix.IoctlGetTermios(int(terminal.Fd()), readTermios)
    if err != nil {
        t.Fatal(err)
    }
    return state
}

func waitHidden(t *testing.T, terminal *os.File) {
    t.Helper()
    deadline := time.Now().Add(3 * time.Second)
    for time.Now().Before(deadline) {
        if terminalState(t, terminal).Lflag&unix.ECHO == 0 {
            return
        }
        time.Sleep(time.Millisecond)
    }
    t.Fatal("secret reader did not disable echo")
}

func TestTerminalSecretRestoresStateAndDoesNotEcho(t *testing.T) {
    for _, cancelRead := range []bool{false, true} {
        t.Run(map[bool]string{false: "success", true: "cancel"}[cancelRead], func(t *testing.T) {
            master, terminal := openTerminal(t)
            before := terminalState(t, terminal)
            ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
            defer cancel()
            type result struct {
                value []byte
                err   error
            }
            done := make(chan result, 1)
            go func() { value, err := ReadTerminalSecret(ctx, terminal, 4096); done <- result{value, err} }()
            waitHidden(t, terminal)
            input := " private password "
            if !cancelRead {
                input += "\n"
            }
            if _, err := io.WriteString(master, input); err != nil {
                t.Fatal(err)
            }
            if cancelRead {
                cancel()
            }
            got := <-done
            defer clear(got.value)
            if cancelRead {
                if !errors.Is(got.err, context.Canceled) || got.value != nil {
                    t.Fatal(got.err)
                }
            } else if got.err != nil || string(got.value) != " private password " {
                t.Fatal("terminal secret bytes changed", got.err)
            }
            if after := terminalState(t, terminal); *after != *before {
                t.Fatal("terminal state was not restored")
            }
            fds := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
            if n, err := unix.Poll(fds, 20); err != nil || n != 0 {
                t.Fatal("terminal echoed secret input", n, err)
            }
            if cancelRead {
                // The unfinished canceled secret must not become the next
                // command's input after the terminal is handed back.
                if _, err := io.WriteString(master, "next\n"); err != nil {
                    t.Fatal(err)
                }
                value, err := readLine(context.Background(), terminal, 4096)
                defer clear(value)
                if err != nil || string(value) != "next" {
                    t.Fatal("canceled secret remained queued", err)
                }
            }
        })
    }
}

type renderedPrompt struct {
    *os.File
    mu    sync.Mutex
    text  bytes.Buffer
    ready chan struct{}
    once  sync.Once
}

func (w *renderedPrompt) Write(p []byte) (int, error) {
    w.mu.Lock()
    defer w.mu.Unlock()
    n, err := w.File.Write(p)
    w.text.Write(p[:n])
    if bytes.Contains(w.text.Bytes(), []byte("Delete payload?")) {
        w.once.Do(func() { close(w.ready) })
    }
    return n, err
}

func TestHuhDefaultNoAndCtrlCRestoreTerminal(t *testing.T) {
    // Only macOS race runs are quarantined; ordinary terminal coverage remains.
    // Re-enable when released dependencies fix the upstream reader shutdown race:
    // https://github.com/charmbracelet/bubbletea/issues/1599
    // https://github.com/charmbracelet/bubbletea/issues/1875
    if skipHuhShutdownRace {
        t.Skip("upstream Bubble Tea/cancelreader shutdown race: https://github.com/charmbracelet/bubbletea/issues/1875")
    }
    for _, tc := range []struct {
        key     string
        columns uint16
    }{{"\r", 80}, {"\x03", 80}, {"\r", 120}, {"\x03", 120}} {
        key := tc.key
        t.Run(fmt.Sprintf("%s-%d", map[string]string{"\r": "default-no", "\x03": "ctrl-c"}[key], tc.columns), func(t *testing.T) {
            master, terminal := openTerminal(t)
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: 24, Cols: tc.columns}); err != nil {
                t.Fatal(err)
            }
            before := terminalState(t, terminal)
            output := &renderedPrompt{File: terminal, ready: make(chan struct{})}
            session := New(Options{In: terminal, Err: output, Interactive: true, Rich: true})
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            type result struct {
                accepted bool
                err      error
            }
            done := make(chan result, 1)
            go func() {
                accepted, err := session.Confirm(ctx, Confirmation{Title: "Delete payload?", Kind: ConfirmDefaultNo, AcceptLabel: "Yes", RejectLabel: "No"})
                done <- result{accepted, err}
            }()
            select {
            case <-output.ready:
            case got := <-done:
                t.Fatalf("Huh exited before rendering: %v", got.err)
            case <-ctx.Done():
                output.mu.Lock()
                rendered := output.text.String()
                output.mu.Unlock()
                t.Fatalf("Huh did not render: %v; output=%q", ctx.Err(), rendered)
            }
            if _, err := io.WriteString(master, key); err != nil {
                t.Fatal(err)
            }
            got := <-done
            if got.accepted {
                t.Fatal("default or canceled confirmation was accepted")
            }
            if key == "\x03" && !errors.Is(got.err, ErrCanceled) {
                t.Fatal(got.err)
            }
            if key == "\r" && got.err != nil {
                t.Fatal(got.err)
            }
            if after := terminalState(t, terminal); *after != *before {
                t.Fatal("Huh did not restore terminal")
            }
        })
    }
}
