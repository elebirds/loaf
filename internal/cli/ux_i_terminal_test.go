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

    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/sdk/agentdelivery"
    "golang.org/x/term"
)

func uxICapture(t *testing.T, output *wizardTerminal, name string) {
    t.Helper()
    if dir := os.Getenv("LOAF_UX_I_CAPTURE_DIR"); dir != "" {
        output.mu.Lock()
        data := append([]byte{}, output.text.Bytes()...)
        output.mu.Unlock()
        if err := os.MkdirAll(dir, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(dir, name+".ansi"), data, 0600); err != nil {
            t.Fatal(err)
        }
    }
}

func TestUXIRichCenterDestinationDetailAndCancel(t *testing.T) {
    for _, tc := range []struct {
        lang       string
        cols, rows uint16
    }{{"en", 80, 18}, {"zh-Hans", 80, 18}, {"en", 80, 24}, {"zh-Hans", 120, 32}} {
        t.Run(fmt.Sprintf("%s-%dx%d", tc.lang, tc.cols, tc.rows), func(t *testing.T) {
            master, terminal, err := pty.Open()
            if err != nil {
                t.Fatal(err)
            }
            drained := make(chan struct{})
            go func() { defer close(drained); io.Copy(io.Discard, master) }()
            t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
            if err = pty.Setsize(terminal, &pty.Winsize{Rows: tc.rows, Cols: tc.cols}); err != nil {
                t.Fatal(err)
            }
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            output := &wizardTerminal{File: terminal}
            c, _ := uxICall(t, tc.lang, "")
            c.app.Notify = &uxINotifyFake{locations: []client.DestinationRecord{{Name: "research", Kind: "codex-desktop", SessionID: "thread_" + strings.Repeat("long_identity_", 14), Experimental: true}}}
            c.options.In, c.options.Err = terminal, output
            c.output.err = output
            l := c.output.locale
            c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help")})
            ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
            done, joined := make(chan error, 1), make(chan struct{})
            go func() { defer close(joined); _, e := c.uxINotify(ctx); done <- e }()
            t.Cleanup(func() { cancel(); <-joined })
            stamp := func() int { output.mu.Lock(); defer output.mu.Unlock(); return output.text.Len() }
            label := fmt.Sprintf("%s-%dx%d", tc.lang, tc.cols, tc.rows)
            uxCoreWaitAfter(t, ctx, output, 0, l.Text("ux_i_notify"))
            uxICapture(t, output, "center-"+label)
            offset := stamp()
            io.WriteString(master, strings.Repeat("\x1b[A", 6)+"\r")
            uxCoreWaitAfter(t, ctx, output, offset, "research")
            uxICapture(t, output, "destinations-"+label)
            offset = stamp()
            io.WriteString(master, strings.Repeat("\x1b[A", 5)+"\r")
            uxCoreWaitAfter(t, ctx, output, offset, l.Text("ux_i_probe"))
            offset = stamp()
            io.WriteString(master, strings.Repeat("\x1b[A", 3)+"\r")
            uxCoreWaitAfter(t, ctx, output, offset, l.Text("ux_i_session"))
            uxICapture(t, output, "detail-"+label)
            // Ctrl+C leaves one screen at a time without mutation: detail,
            // location actions, location browser, then the notification hub.
            offset = stamp()
            io.WriteString(master, "\x03")
            uxCoreWaitAfter(t, ctx, output, offset, l.Text("ux_i_probe"))
            offset = stamp()
            io.WriteString(master, "\x03")
            uxCoreWaitAfter(t, ctx, output, offset, l.Text("ux_i_bind"))
            offset = stamp()
            io.WriteString(master, "\x03")
            uxCoreWaitAfter(t, ctx, output, offset, l.Text("ux_i_notify"))
            io.WriteString(master, "\x03")
            select {
            case err := <-done:
                if err != nil {
                    t.Fatal(err)
                }
            case <-ctx.Done():
                t.Fatal(output.snapshot())
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) {
                t.Fatal("terminal ownership not restored", err)
            }
        })
    }
}

func TestUXIRichEvidencePagination(t *testing.T) {
    master, terminal, err := pty.Open()
    if err != nil {
        t.Fatal(err)
    }
    drained := make(chan struct{})
    go func() { defer close(drained); io.Copy(io.Discard, master) }()
    t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
    if err = pty.Setsize(terminal, &pty.Winsize{Rows: 18, Cols: 80}); err != nil {
        t.Fatal(err)
    }
    output := &wizardTerminal{File: terminal}
    c, _ := uxICall(t, "zh-Hans", "")
    c.options.In, c.options.Err = terminal, output
    c.output.err = output
    c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: c.output.locale.Text("ux_core_form_help"), SelectHelp: c.output.locale.Text("ux_core_select_help")})
    detail := client.DeliveryDetail{Delivery: client.Delivery{ID: "delivery_fixture", EventID: "event_fixture", State: "unconfirmed", Receipt: agentdelivery.Receipt{State: "written"}}, Recipient: client.DestinationRecord{Name: "research", Kind: "claude-channel", SessionID: "fixture_session"}, Event: event.Envelope{ID: "event_fixture", OccurredAt: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)}}
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    done, joined := make(chan error, 1), make(chan struct{})
    go func() { defer close(joined); done <- c.uxShowDocument(ctx, c.uxIDeliveryDocument(detail)) }()
    t.Cleanup(func() { cancel(); <-joined })
    uxCoreWaitAfter(t, ctx, output, 0, "第 1 /")
    uxICapture(t, output, "evidence-page1-zh-Hans-80x18")
    for page := 2; page <= 3; page++ {
        output.mu.Lock()
        start := output.text.Len()
        output.mu.Unlock()
        up := 1
        if page > 2 {
            up = 2
        }
        io.WriteString(master, strings.Repeat("\x1b[A", up)+"\r")
        uxCoreWaitAfter(t, ctx, output, start, fmt.Sprintf("第 %d /", page))
        uxICapture(t, output, fmt.Sprintf("evidence-page%d-zh-Hans-80x18", page))
    }
    io.WriteString(master, "\x03")
    if err := <-done; !errors.Is(err, interaction.ErrCanceled) {
        t.Fatal(err)
    }
}
