package cli

import (
    "context"
    "fmt"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/testutil"
    "golang.org/x/sys/windows"
    "golang.org/x/term"
)

func TestWindowsExplicitConsoleEntry(t *testing.T) {
    if len(os.Args) >= 4 && os.Args[len(os.Args)-3] == "loaf-ui-child" {
        windowsUIChild(t, os.Args[len(os.Args)-2], os.Args[len(os.Args)-1])
        return
    }
    for _, language := range []string{"en", "zh-Hans"} {
        for _, mode := range []string{"rich", "plain", "dumb", "bare", "json"} {
            t.Run(language+"/"+mode, func(t *testing.T) {
                columns, rows := int16(80), int16(18)
                if language == "zh-Hans" {
                    columns, rows = 120, 24
                }
                console := testutil.ConsoleProcess(t, columns, rows, "-test.run=^TestWindowsExplicitConsoleEntry$", "--", "loaf-ui-child", language, mode)
                if mode == "rich" {
                    l, _ := i18n.New(language, nil)
                    console.Wait(t, l.Text("ux_a_hub"))
                    console.Input.WriteString("\r") // Default action is Back.
                } else if mode == "plain" || mode == "dumb" {
                    console.Wait(t, "> ")
                    console.Input.WriteString("4\r")
                }
                // A bare command and JSON must finish in a real console without
                // receiving any input, including an Agent-owned ConPTY.
                console.Wait(t, "DONE")
                if mode == "json" && !strings.Contains(console.Text(), `"ok":true`) {
                    t.Fatal(console.Text())
                }
            })
        }
    }
}

func windowsUIChild(t *testing.T, language, mode string) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("NO_COLOR", "")
    o := fixture(t)
    o.In, o.Out, o.Err = os.Stdin, os.Stdout, os.Stderr
    o.Interactive = term.IsTerminal(int(os.Stdin.Fd()))
    o.TerminalOutput = term.IsTerminal(int(os.Stdout.Fd()))
    o.TerminalError = term.IsTerminal(int(os.Stderr.Fd()))
    if !o.Interactive || !o.TerminalError {
        t.Fatal("owned ConPTY was not a native terminal")
    }
    o.Env = map[string]string{"TERM": "xterm-256color"}
    switch mode {
    case "plain":
        t.Setenv("NO_COLOR", "1")
        o.Env["NO_COLOR"] = "1"
    case "dumb":
        t.Setenv("TERM", "dumb")
        o.Env["TERM"] = "dumb"
    }
    handle := windows.Handle(os.Stdin.Fd())
    var before uint32
    if err := windows.GetConsoleMode(handle, &before); err != nil {
        t.Fatal(err)
    }
    args := []string{"--lang", language, "machine"}
    if mode == "json" {
        args = append(args, "--json")
    } else if mode != "bare" {
        args = append(args, "-i")
    }
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    if code := execute(ctx, args, o); code != 0 {
        t.Fatal("console entry failed", code)
    }
    var after uint32
    if err := windows.GetConsoleMode(handle, &after); err != nil || before != after {
        t.Fatal("console was not restored", before, after, err)
    }
    fmt.Println("DONE")
}
