package interaction

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/testutil"
    "golang.org/x/sys/windows"
)

func TestWindowsNativeHandoffRestoresConsoleAndNextForm(t *testing.T) {
    if len(os.Args) >= 3 && os.Args[len(os.Args)-2] == "loaf-native-child" {
        windowsNativeChild(t, os.Args[len(os.Args)-1])
        return
    }
    dir := t.TempDir()
    console := testutil.ConsoleProcess(t, 80, 18, "-test.run=^TestWindowsNativeHandoffRestoresConsoleAndNextForm$", "--", "loaf-native-child", dir)
    console.Wait(t, "Working")
    if err := os.WriteFile(filepath.Join(dir, "native"), nil, 0600); err != nil {
        t.Fatal(err)
    }
    console.Wait(t, "NATIVE_PROMPT")
    console.Input.WriteString("approved 中文\r")
    console.Wait(t, "Next form")
    console.Input.WriteString("\r")
    console.Wait(t, "DONE")
}

func windowsNativeChild(t *testing.T, dir string) {
    var before uint32
    handle := windows.Handle(os.Stdin.Fd())
    if err := windows.GetConsoleMode(handle, &before); err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    session := New(Options{In: os.Stdin, Err: os.Stderr, Interactive: true, Rich: true})
    live, err := session.StartProgress(ctx, cancel, ProgressFrame{Document: Document{Title: "Working"}})
    if err != nil || live == nil {
        t.Fatal(live, err)
    }
    for {
        if _, err := os.Stat(filepath.Join(dir, "native")); err == nil {
            break
        }
        if ctx.Err() != nil {
            t.Fatal(ctx.Err())
        }
        time.Sleep(time.Millisecond)
    }
    var calls int
    err = session.WithNative(ctx, live, func() error {
        calls++
        var mode uint32
        if err := windows.GetConsoleMode(handle, &mode); err != nil || mode != before {
            t.Fatal("renderer retained console", mode, before, err)
        }
        fmt.Println("NATIVE_PROMPT")
        line, err := readLine(ctx, os.Stdin, 128)
        if err != nil || string(line) != "approved 中文" {
            t.Fatal("native input changed", string(line), err)
        }
        return err
    })
    if err != nil || calls != 1 {
        t.Fatal("native callback replayed or failed", calls, err)
    }
    if err := session.Page(ctx, "Next form", &Field{Title: "Continue", Choices: []Choice{{"Done", "done"}}}); err != nil {
        t.Fatal(err)
    }
    var after uint32
    if err := windows.GetConsoleMode(handle, &after); err != nil || before != after {
        t.Fatal("console not restored", before, after, err)
    }
    fmt.Println("DONE")
}
