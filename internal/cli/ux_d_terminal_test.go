//go:build !windows

package cli

import (
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "regexp"
    "strings"
    "testing"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/task"
    "golang.org/x/term"
)

type uxDTerminal struct{ wizardTerminal }

// Capture sparse fmt writes as well as renderer writes; os.File.WriteString
// would otherwise bypass the wrapper through method promotion.
func (w *uxDTerminal) WriteString(value string) (int, error) { return w.Write([]byte(value)) }

func uxDCapture(t *testing.T, output *uxDTerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_D_CAPTURE_DIR"); directory != "" {
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

func uxDThemeFrame(t *testing.T, ctx context.Context, output *uxDTerminal, offset int, light bool) {
    t.Helper()
    color := "38;2;94;234;212"
    if light {
        color = "38;2;15;118;110"
    }
    for {
        output.mu.Lock()
        ready := strings.Contains(output.text.String()[offset:], color)
        output.mu.Unlock()
        if ready {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatal("terminal did not render the requested background theme")
        case <-time.After(time.Millisecond):
        }
    }
}

func uxDWaitScreen(t *testing.T, ctx context.Context, output *uxDTerminal, offset int, title, footer string) {
    t.Helper()
    // A document title also appears as a menu choice on the preceding screen.
    // Match a title line, not that choice or a final repaint of the old screen.
    // A renderer can start a new title with CR rather than LF.
    heading := regexp.MustCompile(`(?:^|[\r\n]) *` + regexp.QuoteMeta(title) + ` *\r?\n`)
    for {
        output.mu.Lock()
        text := ansi.Strip(output.text.String()[offset:])
        output.mu.Unlock()
        if match := heading.FindStringIndex(text); match != nil && strings.Contains(text[match[1]:], footer) {
            return
        }
        select {
        case <-ctx.Done():
            t.Fatalf("missing complete screen %q: %s", title, text)
        case <-time.After(time.Millisecond):
        }
    }
}

func TestUXDRichBrowseDetailsRefreshCancelStop(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("COLORTERM", "truecolor")
    t.Setenv("NO_COLOR", "")
    for _, entry := range []struct {
        language      string
        rows, columns uint16
        light         bool
        partial       bool
    }{{"en", 18, 80, false, false}, {"zh-Hans", 18, 80, false, false}, {"en", 32, 120, true, false}, {"zh-Hans", 24, 80, true, false}, {"zh-Hans", 18, 80, false, true}} {
        name := fmt.Sprintf("%s-%dx%d", entry.language, entry.columns, entry.rows)
        if entry.partial {
            name = "partial-" + name
        }
        t.Run(name, func(t *testing.T) {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            drained := make(chan struct{})
            go func() {
                defer close(drained)
                var pending string
                buffer := make([]byte, 4096)
                for {
                    n, err := master.Read(buffer)
                    if err != nil {
                        return
                    }
                    pending += string(buffer[:n])
                    for strings.Contains(pending, "\x1b]11;?") {
                        index := strings.Index(pending, "\x1b]11;?")
                        pending = pending[index+len("\x1b]11;?"):]
                        shade := "0000"
                        if entry.light {
                            shade = "ffff"
                        }
                        _, _ = io.WriteString(master, fmt.Sprintf("\x1b]11;rgb:%s/%s/%s\x1b\\", shade, shade, shade))
                    }
                    if len(pending) > 32 {
                        pending = pending[len(pending)-32:]
                    }
                }
            }()
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: entry.rows, Cols: entry.columns}); err != nil {
                t.Fatal(err)
            }
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            output := &uxDTerminal{wizardTerminal: wizardTerminal{File: terminal}}
            o, remote, refs := psFixture(t)
            o.Runs.Metadata = o.State
            if entry.partial {
                remote.failID = refs[0].ID
            }
            for i, ref := range refs {
                if err := o.State.SaveRunMetadata(context.Background(), ref.ProjectID, ref.ID, task.RunMetadata{Name: fmt.Sprintf("中文 experiment %d · long name", i), Tags: []string{"seed 1", "ablation"}}); err != nil {
                    t.Fatal(err)
                }
            }
            c := uxDCall(t, o, entry.language, "")
            backIndex, stopIndex := uxDDetailIndices(t, c, refs[0].ID)
            c.options.In, c.options.Err = terminal, output
            c.output.err = output
            l := c.output.locale
            c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true,
                PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help"), MultiHelp: l.Text("ux_core_multi_help")})
            ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() { defer close(joined); _, err := c.uxDTasks(ctx); done <- err }()
            t.Cleanup(func() { cancel(); <-joined })
            uxCoreWaitAfter(t, ctx, &output.wizardTerminal, 0, l.Text("ux_core_select_help"))
            uxDThemeFrame(t, ctx, output, 0, entry.light)
            uxDCapture(t, output, "list-"+name)
            step := func(keys, want, capture string) {
                t.Helper()
                output.mu.Lock()
                start := output.text.Len()
                output.mu.Unlock()
                if _, err := io.WriteString(master, keys); err != nil {
                    t.Fatal(err)
                }
                if want == l.Text("ux_d_stop_confirm") {
                    uxCoreWaitFrame(t, ctx, &output.wizardTerminal, start, want, l.Text("ux_core_form_help"))
                } else {
                    uxDWaitScreen(t, ctx, output, start, want, l.Text("ux_core_select_help"))
                }
                if capture != "" {
                    uxDThemeFrame(t, ctx, output, start, entry.light)
                    uxDCapture(t, output, capture+"-"+name)
                }
            }
            // Match screen titles, not fields also present on other pages.
            // Wait for the complete frame before sending the next key batch.
            title := func(id string) string { return l.Text(id) }
            step("\r", title("ux_d_task"), "task")
            step("\r", title("ux_d_details"), "facts")
            // The document has multiple pages on short terminals; its default
            // action is always Back, independently of page count.
            step("\r", title("ux_d_task"), "")
            step(strings.Repeat("\x1b[B", backIndex-1)+"\r", title("ux_d_tasks"), "returned")
            size := max(1, min(5, (int(entry.rows)-13)/2))
            rowCount := min(size, len(refs))
            actionIndex := rowCount
            if rowCount < len(refs) {
                actionIndex++
            }
            step(strings.Repeat("\x1b[B", actionIndex)+"\r", title("ux_d_actions"), "actions")
            step(strings.Repeat("\x1b[A", 4)+"\r", title("ux_d_tasks"), "refreshed")
            step("\r", title("ux_d_task"), "observed-task")
            step(strings.Repeat("\x1b[B", stopIndex-1)+"\r", l.Text("ux_d_stop_confirm"), "stop-review")
            step("\r", title("ux_d_task"), "stop-canceled")
            step(strings.Repeat("\x1b[B", backIndex-stopIndex)+"\r", title("ux_d_tasks"), "")
            if _, err := io.WriteString(master, strings.Repeat("\x1b[B", actionIndex+1)+"\r"); err != nil {
                t.Fatal(err)
            }
            select {
            case err := <-done:
                if err != nil {
                    t.Fatal(err, output.snapshot())
                }
            case <-ctx.Done():
                t.Fatal("task browser did not return", output.snapshot())
            }
            if remote.calls != 4 || remote.req.Action != "" {
                t.Fatal("opening a list/detail or canceling stop mutated or queried", remote.calls, remote.req)
            }
            if c.input.Dark() == entry.light {
                t.Fatal("background response was not consumed")
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) {
                t.Fatal("terminal not restored", err)
            }
        })
    }
}
