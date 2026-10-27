package interaction

import (
    "context"
    "errors"
    "io"
    "sync"

    "charm.land/huh/v2"
)

func (s *InputSession) richText(ctx context.Context, prompt TextPrompt, limit int) (string, error) {
    var value string
    // Keep one overflow character so a long paste fails instead of silently
    // submitting a truncated connection string or confirmation.
    field := huh.NewInput().Title(prompt.Title).Description(prompt.Description).CharLimit(limit + 1).Value(&value)
    if err := s.runForm(ctx, huh.NewForm(huh.NewGroup(field))); err != nil {
        return "", err
    }
    // Huh counts characters, while the protocol's bounds are UTF-8 bytes.
    if len(value) > limit {
        return "", ErrInputTooLong
    }
    return value, nil
}

func (s *InputSession) richConfirm(ctx context.Context, prompt Confirmation) (bool, error) {
    value := false
    field := huh.NewConfirm().Title(prompt.Title).Description(prompt.Description).
        Affirmative(prompt.AcceptLabel).Negative(prompt.RejectLabel).Value(&value)
    if err := s.runForm(ctx, huh.NewForm(huh.NewGroup(field))); err != nil {
        return false, err
    }
    return value, nil
}

func (s *InputSession) runForm(ctx context.Context, form *huh.Form) error {
    runCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    output := &formWriter{writer: s.options.Err, cancel: cancel}
    var writer io.Writer = output
    if terminal, ok := s.options.Err.(terminalOutput); ok {
        // Bubble Tea discovers width and resize events through the output fd.
        // A plain io.Writer wrapper would hide it and produce a blank view.
        writer = terminalFormWriter{terminalOutput: terminal, checked: output}
    }
    // Never enter Huh's accessible path: it does not propagate context or field
    // errors. The caller chooses our cancellable plain renderer instead.
    err := form.WithAccessible(false).WithShowHelp(false).
        WithInput(s.options.In).WithOutput(writer).RunWithContext(runCtx)
    if writeErr := output.failure(); writeErr != nil {
        return writeErr
    }
    if ctx.Err() != nil {
        return ctx.Err()
    }
    if errors.Is(err, huh.ErrUserAborted) {
        return ErrCanceled
    }
    return err
}

type terminalOutput interface {
    io.ReadWriteCloser
    Fd() uintptr
}

type terminalFormWriter struct {
    terminalOutput
    checked *formWriter
}

func (w terminalFormWriter) Write(p []byte) (int, error) { return w.checked.Write(p) }
func (w terminalFormWriter) Close() error                { return nil } // The session does not own stderr.

// Bubble Tea renders on another goroutine. Preserve a failed/short terminal
// write and stop the form instead of silently accepting an unseen prompt.
type formWriter struct {
    mu     sync.Mutex
    writer io.Writer
    cancel context.CancelFunc
    err    error
}

func (w *formWriter) Write(p []byte) (int, error) {
    w.mu.Lock()
    defer w.mu.Unlock()
    if w.err != nil {
        return 0, w.err
    }
    n, err := w.writer.Write(p)
    if err == nil && n != len(p) {
        err = io.ErrShortWrite
    }
    if err != nil {
        w.err = err
        w.cancel()
    }
    return n, err
}

func (w *formWriter) failure() error {
    w.mu.Lock()
    defer w.mu.Unlock()
    return w.err
}
