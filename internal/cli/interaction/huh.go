package interaction

import (
    "context"
    "io"
    "sync"

    tea "charm.land/bubbletea/v2"
    "charm.land/huh/v2"
)

func (s *InputSession) richText(ctx context.Context, prompt TextPrompt, limit int) (string, error) {
    var value string
    // Keep one overflow character so a long paste fails instead of silently
    // submitting a truncated connection string or confirmation.
    field := huh.NewInput().Title(prompt.Title).Description(prompt.Description).CharLimit(limit + 1).Value(&value)
    if prompt.Validate != nil {
        field.Validate(prompt.Validate)
    }
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
    // Embed the form so cancellation uses a graceful Quit, not Bubble Tea's
    // killed path, which closes the input reader without joining its worker.
    // The command owns signals and context; the form still owns its fields.
    form.WithAccessible(false).WithShowHelp(false)
    form.SubmitCmd, form.CancelCmd = tea.Quit, tea.Quit
    _, err := tea.NewProgram(formModel{form: form, ctx: runCtx},
        tea.WithInput(s.options.In), tea.WithOutput(writer), tea.WithoutSignalHandler(),
        tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
            // Bubble Tea sends initial size and resize events itself. Huh's
            // redundant request starts an unjoined fd query during shutdown.
            if msg == tea.RequestWindowSize() {
                return nil
            }
            return msg
        })).Run()
    if writeErr := output.failure(); writeErr != nil {
        return writeErr
    }
    if ctx.Err() != nil {
        return ctx.Err()
    }
    if form.State == huh.StateAborted {
        return ErrCanceled
    }
    return err
}

type formCanceled struct{}

// formModel adapts Huh's string view to Bubble Tea's view and serializes context
// cancellation with field updates, allowing the terminal reader to finish first.
type formModel struct {
    form *huh.Form
    ctx  context.Context
}

func (m formModel) Init() tea.Cmd {
    return tea.Batch(m.form.Init(), func() tea.Msg {
        <-m.ctx.Done()
        return formCanceled{}
    })
}

func (m formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if _, canceled := msg.(formCanceled); canceled {
        return m, tea.Quit
    }
    form, cmd := m.form.Update(msg)
    m.form = form.(*huh.Form)
    return m, cmd
}

func (m formModel) View() tea.View {
    view := tea.NewView(m.form.View())
    view.ReportFocus = true
    return view
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
