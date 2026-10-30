package interaction

import (
    "context"
    "io"
    "strings"
    "sync"

    tea "charm.land/bubbletea/v2"
    "charm.land/huh/v2"
    "charm.land/lipgloss/v2"
    "github.com/charmbracelet/x/ansi"
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

func (s *InputSession) richSummaryConfirmation(ctx context.Context, prompt Confirmation) (string, error) {
    var value string
    form := summaryConfirmationForm(prompt, Width(s.options.Err), &value, s.Dark())
    if err := s.runForm(ctx, form); err != nil {
        return "", err
    }
    if len(value) > 64 {
        return "", ErrInputTooLong
    }
    return value, nil
}

func summaryConfirmationForm(prompt Confirmation, width int, value *string, dark bool) *huh.Form {
    field := huh.NewInput().Title(prompt.Title).CharLimit(65).Value(value)
    summary := *prompt.Summary
    title := summary.Title
    summary.Title = ""
    description := summary.Content(max(20, min(width, 96)-8), false, dark)
    return huh.NewForm(huh.NewGroup(field).Title(title).Description(description))
}

func (s *InputSession) runForm(ctx context.Context, form *huh.Form, header ...string) error {
    runCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    input, closeInput := richInput(s.options.In)
    defer closeInput()
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
    if s.options.Compact {
        form.WithTheme(huh.ThemeFunc(formTheme)).WithWidth(max(20, min(Width(s.options.Err), 96)-4))
        form.WithHeight(max(5, Height(s.options.Err)-4))
    }
    form.SubmitCmd, form.CancelCmd = tea.Quit, tea.Quit
    footer := ""
    if s.options.Compact {
        footer = s.options.PageHelp
    }
    title, summary := "", ""
    if len(header) == 2 {
        title, summary = header[0], header[1]
    }
    program := tea.NewProgram(formModel{form: form, height: Height(s.options.Err), headerTitle: title, headerSummary: summary, ctx: runCtx, footer: footer, selectFooter: s.options.SelectHelp, multiFooter: s.options.MultiHelp, compact: s.options.Compact, width: Width(s.options.Err), dark: s.Dark(), background: s.dark.Store},
        tea.WithInput(input), tea.WithOutput(writer), tea.WithoutSignalHandler(),
        tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
            // Bubble Tea sends initial size and resize events itself. Huh's
            // redundant request starts an unjoined fd query during shutdown.
            if msg == tea.RequestWindowSize() {
                return nil
            }
            return msg
        }))
    stopResize := watchRichSize(program, s.options.Err)
    defer stopResize()
    _, err := program.Run()
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
    headerTitle, headerSummary string
    height, detailOffset       int
    form                       *huh.Form
    ctx                        context.Context
    footer                     string
    selectFooter, multiFooter  string
    compact                    bool
    finished                   bool
    width                      int
    dark                       bool
    background                 func(bool)
}

func (m formModel) Init() tea.Cmd {
    return tea.Batch(m.form.Init(), tea.RequestBackgroundColor, func() tea.Msg {
        <-m.ctx.Done()
        return formCanceled{}
    })
}

func (m formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if key, ok := msg.(tea.KeyPressMsg); ok && m.overflowInput() {
        switch key.String() {
        case "pgdown":
            m.detailOffset += 3
            return m, nil
        case "pgup":
            m.detailOffset = max(0, m.detailOffset-3)
            return m, nil
        }
    }
    if bg, ok := msg.(tea.BackgroundColorMsg); ok {
        m.dark = bg.IsDark()
        if m.background != nil {
            m.background(m.dark)
        }
    }
    if _, canceled := msg.(formCanceled); canceled {
        m.finished = true
        return m, tea.Quit
    }
    if v, ok := msg.(tea.WindowSizeMsg); ok && m.compact {
        if v.Height > 0 {
            m.height = v.Height
        } else {
            v.Height = m.height
        }
        if v.Width > 0 {
            m.width = v.Width
        } else {
            v.Width = m.width
        }
        m.form.WithWidth(max(20, min(v.Width, 96)-4))
        v.Height = max(5, v.Height-4)
        msg = v
    }
    focused := m.form.GetFocusedField()
    form, cmd := m.form.Update(msg)
    m.form = form.(*huh.Form)
    if focused != m.form.GetFocusedField() {
        m.detailOffset = 0
    }
    return m, cmd
}

func (m formModel) View() tea.View {
    if m.finished || m.form.State != huh.StateNormal {
        return tea.NewView("")
    }
    content := m.form.View()
    if m.overflowInput() {
        content = m.shortInputView()
    }
    if m.compact {
        content = strings.TrimRight(trimLines(content), "\n")
    }
    if content != "" && m.footer != "" {
        footer := m.footer
        if m.overflowInput() {
            footer = "PgUp/PgDn · " + footer
        }
        switch m.form.GetFocusedField().(type) {
        case *huh.Select[string]:
            if m.selectFooter != "" {
                footer = m.selectFooter
            }
        case *huh.MultiSelect[string]:
            if m.multiFooter != "" {
                footer = m.multiFooter
            }
        }
        width := m.width
        if width <= 0 {
            width = 80
        }
        help := lipgloss.NewStyle().Foreground(colors(m.dark).muted).Width(max(20, min(width, 96)-4)).MarginLeft(2).Render(footer)
        content += "\n\n" + trimLines(help) + "\n"
    }
    view := tea.NewView(content)
    view.ReportFocus = true
    return view
}

// Huh scrolls between fields, not within a field. Keep the input visible when a
// review description is taller than a short terminal; page only its details.
func (m formModel) overflowInput() bool {
    if !m.compact || m.height == 0 {
        return false
    }
    field, ok := m.form.GetFocusedField().(*huh.Input)
    if !ok {
        return false
    }
    header := lipgloss.Height(m.shortHeader()) + 3
    return lipgloss.Height(field.View()) > max(3, m.height-header-4)
}

func (m formModel) shortHeader() string {
    return (Document{Title: m.headerTitle, Context: strings.ReplaceAll(m.headerSummary, "\n\n", "\n")}).Content(max(20, min(m.width, 96)-4), true, m.dark)
}

func (m formModel) shortInputView() string {
    header := m.shortHeader()
    lines := strings.Split(strings.TrimRight(trimLines(m.form.GetFocusedField().View()), "\n"), "\n")
    for len(lines) > 1 && strings.Trim(strings.TrimSpace(ansi.Strip(lines[len(lines)-1])), "┃│ ") == "" {
        lines = lines[:len(lines)-1]
    }
    if len(lines) < 3 {
        return header + "\n" + strings.Join(lines, "\n")
    }
    keep := max(1, m.height-lipgloss.Height(header)-7)
    body := lines[1 : len(lines)-1]
    offset := min(m.detailOffset, max(0, len(body)-keep))
    end := min(len(body), offset+keep)
    return lipgloss.NewStyle().MarginLeft(2).Render(header) + "\n\n" + lines[0] + "\n" + strings.Join(body[offset:end], "\n") + "\n" + lines[len(lines)-1]
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
