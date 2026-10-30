package interaction

import (
    "context"
    "errors"
    "os"
    "testing"
    "time"
)

func TestWindowsPipeCancellationDoesNotConsumeNextPrompt(t *testing.T) {
    input, output, err := os.Pipe()
    if err != nil {
        t.Fatal(err)
    }
    defer input.Close()
    defer output.Close()
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
    defer cancel()
    if _, err := readLine(ctx, input, 1024); !errors.Is(err, context.DeadlineExceeded) {
        t.Fatal(err)
    }
    go func() { output.WriteString("中文 next\r\n") }()
    next, end := context.WithTimeout(context.Background(), 2*time.Second)
    defer end()
    value, err := readLine(next, input, 1024)
    if err != nil || string(value) != "中文 next" {
        t.Fatal(string(value), err)
    }
    clear(value)
    if _, err := ReadTerminalSecret(next, input, 1024); !errors.Is(err, ErrTerminalRequired) {
        t.Fatal("secret accepted a pipe", err)
    }
}
