package interaction

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "testing"
    "time"

    tea "charm.land/bubbletea/v2"
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
    console.Wait(t, "RESIZE_WAIT")
    console.Resize(t, 120, 30)
    console.Wait(t, "RESIZED")
    console.Wait(t, "Working")
    if err := os.WriteFile(filepath.Join(dir, "native"), nil, 0600); err != nil {
        t.Fatal(err)
    }
    console.Wait(t, "NATIVE_PROMPT")
    console.Input.WriteString("approved 中文\r")
    console.Wait(t, "Next form")
    console.Input.WriteString("\r")
    console.Wait(t, "ARROWS")
    console.Input.WriteString("\x1b[B\r")
    console.Wait(t, "RICH_UNICODE")
    console.Input.WriteString("中文 🔑\r")
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
    input, closeInput := richInput(os.Stdin)
    program := tea.NewProgram(windowsResizeModel{}, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(os.Stdout), tea.WithoutSignalHandler())
    stopResize := watchRichSize(program, os.Stdout)
    _, err := program.Run()
    stopResize()
    closeInput()
    if err != nil {
        t.Fatal(err)
    }
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
    selected, err := session.Select(ctx, "ARROWS", []Choice{{"First", "one"}, {"Second", "two"}}, "one")
    if err != nil || selected != "two" {
        t.Fatal("rich arrow input changed", selected, err)
    }
    text, err := session.Text(ctx, TextPrompt{Title: "RICH_UNICODE"})
    if err != nil || text != "中文 🔑" {
        t.Fatal("rich input changed Unicode", text, err)
    }
    var after uint32
    if err := windows.GetConsoleMode(handle, &after); err != nil || before != after {
        t.Fatal("console not restored", before, after, err)
    }
    fmt.Println("DONE")
}

type windowsResizeModel struct{ resized bool }

func (m windowsResizeModel) Init() tea.Cmd { return nil }
func (m windowsResizeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if size, ok := msg.(tea.WindowSizeMsg); ok && size.Width == 120 && size.Height == 30 {
        m.resized = true
        return m, tea.Quit
    }
    return m, nil
}
func (m windowsResizeModel) View() tea.View {
    if m.resized {
        return tea.NewView("RESIZED\n")
    }
    return tea.NewView("RESIZE_WAIT\n")
}
