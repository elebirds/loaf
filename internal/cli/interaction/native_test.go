package interaction

import (
    "context"
    "errors"
    "io"
    "strings"
    "testing"
    "time"
)

func TestNativeOwnershipIsSerialAndCancellationDoesNotReplay(t *testing.T) {
    session := New(Options{})
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    entered, release := make(chan struct{}), make(chan struct{})
    first := make(chan error, 1)
    marker := errors.New("native result")
    go func() {
        first <- session.WithNative(context.Background(), nil, func() error {
            close(entered)
            <-release
            return marker
        })
    }()
    <-entered
    called := false
    cancel()
    if err := session.WithNative(ctx, nil, func() error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
        t.Fatal("canceled operation ran", called, err)
    }
    close(release)
    if err := <-first; !errors.Is(err, marker) {
        t.Fatal(err)
    }
    if err := session.WithNative(context.Background(), nil, func() error { called = true; return nil }); err != nil || !called {
        t.Fatal("input owner was not released", called, err)
    }
}

func TestNativeHandoffJoinsProgressAndRestoresTerminal(t *testing.T) {
    master, terminal := openTerminal(t)
    drainTerminal(t, master, terminal)
    before := terminalState(t, terminal)
    output := &renderedPrompt{File: terminal, title: "Working", ready: make(chan struct{})}
    session := New(Options{In: terminal, Err: output, Interactive: true, Rich: true})
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    live, err := session.StartProgress(ctx, cancel, ProgressFrame{Document: Document{Title: "Working"}})
    if err != nil || live == nil {
        t.Fatal(live, err)
    }
    waitRendered(t, ctx, output, "Working")
    done := make(chan error, 1)
    go func() {
        done <- session.WithNative(ctx, live, func() error {
            if current := terminalState(t, terminal); *current != *before {
                return errors.New("native started before terminal restoration")
            }
            if _, err := output.Write([]byte("Native prompt: ")); err != nil {
                return err
            }
            line, err := readLine(ctx, terminal, 64)
            if err != nil {
                return err
            }
            if strings.TrimSpace(string(line)) != "approved" {
                return errors.New("renderer consumed native input")
            }
            return nil
        })
    }()
    waitRendered(t, ctx, output, "Native prompt: ")
    if _, err := io.WriteString(master, "approved\n"); err != nil {
        t.Fatal(err)
    }
    select {
    case err := <-done:
        if err != nil {
            t.Fatal(err)
        }
    case <-ctx.Done():
        t.Fatal("native input did not finish")
    }
    if current := terminalState(t, terminal); *current != *before {
        t.Fatal("handoff did not leave native terminal settings")
    }
    next := make(chan error, 1)
    go func() {
        next <- session.Page(ctx, "Next form", &Field{Title: "Continue", Choices: []Choice{{"Done", "done"}}})
    }()
    waitRendered(t, ctx, output, "Next form")
    if _, err := io.WriteString(master, "\r"); err != nil {
        t.Fatal(err)
    }
    if err := <-next; err != nil {
        t.Fatal("next input owner did not resume", err)
    }
}
