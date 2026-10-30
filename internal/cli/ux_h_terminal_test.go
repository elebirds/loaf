package cli

import (
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
    "github.com/elebirds/loaf/internal/queue"
    "golang.org/x/term"
)

type uxHTerminalWriter struct{ *wizardTerminal }

func (w uxHTerminalWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func uxHTerminal(t *testing.T, language string, cols, rows uint16, spy *uxHQueueSpy) (*invocation, *os.File, *os.File, *wizardTerminal) {
    t.Helper()
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    drained := make(chan struct{})
    go func() { defer close(drained); _, _ = io.Copy(io.Discard, master) }()
    t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
    if err := pty.Setsize(terminal, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
        t.Fatal(err)
    }
    output := &wizardTerminal{File: terminal}
    c := uxHCallFixture(t, language, "", spy)
    writer := uxHTerminalWriter{output}
    c.options.In, c.options.Err, c.output.err = terminal, writer, writer
    c.input = interaction.New(interaction.Options{In: terminal, Err: writer, Interactive: true, Rich: true, Compact: true, PageHelp: c.output.locale.Text("ux_core_form_help"), SelectHelp: c.output.locale.Text("ux_core_select_help")})
    return c, master, terminal, output
}

func uxHWaitFrame(t *testing.T, ctx context.Context, output *wizardTerminal, offset int, value string) int {
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
            t.Fatalf("missing frame %q: %s", value, output.snapshot())
        case <-time.After(time.Millisecond):
        }
    }
}

func uxHCapture(t *testing.T, output *wizardTerminal, name string) {
    t.Helper()
    if directory := os.Getenv("LOAF_UX_H_CAPTURE_DIR"); directory != "" {
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

func TestUXHRichLongListDetailBackAndPaging(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("COLORTERM", "truecolor")
    t.Setenv("NO_COLOR", "")
    for _, size := range []struct {
        language   string
        cols, rows uint16
    }{{"en", 80, 18}, {"zh-Hans", 80, 18}, {"en", 120, 32}, {"zh-Hans", 80, 24}} {
        t.Run(fmt.Sprintf("%s-%dx%d", size.language, size.cols, size.rows), func(t *testing.T) {
            spy := &uxHQueueSpy{}
            c, master, terminal, output := uxHTerminal(t, size.language, size.cols, size.rows, spy)
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            result := queue.Result{HasMore: true, Status: &queue.Status{Slots: 1, Reserved: 1, Paused: true}}
            for i := range 50 {
                result.Requests = append(result.Requests, queue.Request{ID: fmt.Sprintf("q_%03d", i), RunID: fmt.Sprintf("r_%03d", i), Name: "实验-with-a-long-name", State: "queued", ProjectID: "foreign"})
            }
            var cause error
            selected, search, page, limit := "", "", 0, 50
            ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
            defer cancel()
            done, joined := make(chan error, 1), make(chan struct{})
            go func() {
                defer close(joined)
                done <- c.uxHRequests(ctx, "lab", "current-project", &result, &cause, &limit, &selected, &search, &page)
            }()
            t.Cleanup(func() { cancel(); <-joined })
            captureLocale := size.language
            if size.cols == 120 {
                captureLocale = "dark-" + captureLocale
            }
            start := uxHWaitFrame(t, ctx, output, 0, c.output.locale.Text("ux_core_select_help"))
            if size.cols == 120 {
                _, _ = io.WriteString(master, "\x1b]11;rgb:0000/0000/0000\x1b\\")
                for !c.input.Dark() {
                    select {
                    case <-ctx.Done():
                        t.Fatal("terminal background response was not consumed")
                    case <-time.After(time.Millisecond):
                    }
                }
                start = uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_requests"))
            }
            uxHCapture(t, output, fmt.Sprintf("requests-%s-%dx%d", captureLocale, size.cols, size.rows))
            io.WriteString(master, "\x1b[B\r") // select second row
            start = uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_request"))
            uxHCapture(t, output, fmt.Sprintf("request-%s-%dx%d", captureLocale, size.cols, size.rows))
            io.WriteString(master, "\x1b[B\r") // back from foreign read-only detail
            start = uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_requests"))
            io.WriteString(master, "\r") // remembered second row
            start = uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_request"))
            output.mu.Lock()
            text := ansi.Strip(output.text.String())
            output.mu.Unlock()
            if strings.Count(text, "q_001") < 2 {
                t.Fatal("return lost request selection", text)
            }
            io.WriteString(master, "\x1b[B\r")
            start = uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_requests"))
            io.WriteString(master, strings.Repeat("\x1b[B", 3)+"\r") // second row -> next page
            start = uxHWaitFrame(t, ctx, output, start, "q_004")
            uxHCapture(t, output, fmt.Sprintf("requests-next-%s-%dx%d", captureLocale, size.cols, size.rows))
            io.WriteString(master, "\x03")
            if err := <-done; !errors.Is(err, interaction.ErrCanceled) {
                t.Fatal(err)
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) || page != 1 || len(spy.lists)+len(spy.shows)+len(spy.submits) != 0 {
                t.Fatal("pagination/navigation queried or failed restoration", err, page, spy)
            }
        })
    }
}

func TestUXHNativeReadCompletesBeforeQueueForm(t *testing.T) {
    spy := &uxHQueueSpy{result: queue.Result{Status: &queue.Status{Slots: 1, CoordinatorRunning: true}}}
    c, master, terminal, output := uxHTerminal(t, "en", 80, 18, spy)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    native := make(chan string, 1)
    spy.onList = func() {
        _, _ = output.Write([]byte("Fixture native authentication: "))
        value, err := interaction.ReadTerminalSecret(ctx, terminal, 128)
        if err != nil {
            native <- err.Error()
            return
        }
        native <- string(value)
        clear(value)
    }
    done, joined := make(chan error, 1), make(chan struct{})
    go func() { defer close(joined); _, err := c.uxHQueueWith(ctx, uxHInputs{}); done <- err }()
    t.Cleanup(func() { cancel(); terminal.Close(); <-joined })
    start := uxHWaitFrame(t, ctx, output, 0, "Fixture native authentication")
    io.WriteString(master, "fixture\n")
    if value := <-native; value != "fixture" {
        t.Fatal("native input was consumed by another renderer", value)
    }
    uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_title"))
    uxHCapture(t, output, "hub-en-80x18")
    io.WriteString(master, strings.Repeat("\x1b[B", 3)+"\r")
    if err := <-done; err != nil || len(spy.lists) != 1 {
        t.Fatal(err, len(spy.lists))
    }
}

func TestUXHRichLocalRequestWithIntegratedRunActions(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("NO_COLOR", "")
    for _, language := range []string{"en", "zh-Hans"} {
        t.Run(language, func(t *testing.T) {
            row := queue.Request{ID: "q_saved", RunID: "r_fixed", ProjectID: "p_saved", State: "queued"}
            spy := &uxHQueueSpy{result: queue.Result{Request: &row, Status: &queue.Status{Slots: 1, Reserved: 1, CoordinatorRunning: true}}}
            c, master, terminal, output := uxHTerminal(t, language, 80, 18, spy)
            linked, _, _, _ := uxFCall(t, language, "")
            c.app.Projects = linked.app.Projects
            if len(c.uxHRunRoutes(row, true)) != 3 {
                t.Fatal("fixture must exercise all integrated Run actions")
            }
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            done, joined := make(chan error, 1), make(chan struct{})
            go func() { defer close(joined); done <- c.uxHRequest(ctx, row, nil, true) }()
            t.Cleanup(func() { cancel(); <-joined })
            start := uxHWaitFrame(t, ctx, output, 0, c.output.locale.Text("ux_core_select_help"))
            uxHCapture(t, output, "request-local-"+language+"-80x18")
            _, _ = io.WriteString(master, "\r")
            start = uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_request_id"))
            uxHCapture(t, output, "request-document-"+language+"-80x18")
            _, _ = io.WriteString(master, "\r") // document defaults to Back
            uxHWaitFrame(t, ctx, output, start, c.output.locale.Text("ux_h_cancel"))
            // The last choice stays reachable with every D/E/F route enabled.
            _, _ = io.WriteString(master, strings.Repeat("\x1b[B", 3+len(c.uxHRunRoutes(row, true)))+"\r")
            if err := <-done; err != nil {
                t.Fatal(err)
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) || len(spy.shows) != 1 || spy.shows[0].ID != row.ID || spy.shows[0].Target != nil || len(spy.cancels)+len(spy.cleans)+len(spy.submits) != 0 {
                t.Fatal("detail navigation lost identity, mutated or failed restoration", err, spy)
            }
        })
    }
}
