package cli

import (
    "bytes"
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
    "golang.org/x/term"
)

// Capture actual help written through a PTY, including its natural wrapping.
// No forms or background input owner should be created by explicit help.
func TestUXJHelpTerminal(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, size := range [][2]uint16{{80, 18}, {80, 24}, {120, 32}} {
            for _, mode := range []string{"dark", "light", "no-color", "dumb"} {
                name := fmt.Sprintf("help-%s-%s-%dx%d", language, mode, size[0], size[1])
                t.Run(name, func(t *testing.T) {
                    master, terminal, err := pty.Open()
                    if err != nil {
                        t.Fatal(err)
                    }
                    t.Cleanup(func() { terminal.Close(); master.Close() })
                    if err := pty.Setsize(terminal, &pty.Winsize{Cols: size[0], Rows: size[1]}); err != nil {
                        t.Fatal(err)
                    }
                    before, err := term.GetState(int(terminal.Fd()))
                    if err != nil {
                        t.Fatal(err)
                    }
                    received := make(chan []byte, 1)
                    go func() {
                        var capture bytes.Buffer
                        buffer := make([]byte, 4096)
                        for {
                            n, err := master.Read(buffer)
                            capture.Write(buffer[:n])
                            if bytes.Contains(capture.Bytes(), []byte("UXJ_CAPTURE_END")) || err != nil {
                                received <- capture.Bytes()
                                return
                            }
                        }
                    }()
                    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                    defer cancel()
                    o := Options{Env: map[string]string{"TERM": "xterm-256color"}, In: uxJNoInput{t}, Out: terminal, Err: io.Discard, Interactive: true, TerminalOutput: true, TerminalError: true}
                    if mode == "light" {
                        o.Env["COLORFGBG"] = "0;15"
                    }
                    if mode == "no-color" {
                        o.Env["NO_COLOR"] = "1"
                    }
                    if mode == "dumb" {
                        o.Env["TERM"] = "dumb"
                    }
                    if code := Execute(ctx, []string{"help", "run", "--lang", language}, o); code != 0 {
                        t.Fatal(code)
                    }
                    after, err := term.GetState(int(terminal.Fd()))
                    if err != nil || !reflect.DeepEqual(before, after) {
                        t.Fatal("help changed terminal state", err)
                    }
                    if _, err := io.WriteString(terminal, "UXJ_CAPTURE_END\n"); err != nil {
                        t.Fatal(err)
                    }
                    var data []byte
                    select {
                    case data = <-received:
                    case <-ctx.Done():
                        master.Close()
                        <-received
                        t.Fatal("PTY output was not drained")
                    }
                    data = bytes.SplitN(data, []byte("UXJ_CAPTURE_END"), 2)[0]
                    if !bytes.Contains(data, []byte("loaf run -- python3 train.py")) || bytes.Contains(data, []byte("\x1b")) || strings.Contains(string(data), "missing message") {
                        t.Fatal(string(data))
                    }
                    if directory := os.Getenv("LOAF_UXJ_CAPTURE_DIR"); directory != "" {
                        if err := os.MkdirAll(directory, 0700); err != nil {
                            t.Fatal(err)
                        }
                        if err := os.WriteFile(filepath.Join(directory, name+".ansi"), data, 0600); err != nil {
                            t.Fatal(err)
                        }
                    }
                })
            }
        }
    }
}
