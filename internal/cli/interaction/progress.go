package interaction

import (
    "context"
    "fmt"
    "io"
    "sync"
    "time"

    tea "charm.land/bubbletea/v2"
)

// ProgressFrame is immutable display content. Its clock is local, not a poll.
type ProgressFrame struct {
    Edit                              func()
    Document                          Document
    Footer, DetailFooter, DetailTitle string
    Detail                            string
    Text, ElapsedLabel, NextLabel     string
    StartedAt                         time.Time
    NextAt                            time.Time
}

// LiveProgress owns terminal input until Close; it must start after all forms.
type LiveProgress struct {
    program *tea.Program
    done    chan error
    once    sync.Once
    writer  *formWriter
}

// StartProgress starts a cancellable, command-owned terminal display.
// A nil result means the caller should use its plain presentation.
func (s *InputSession) StartProgress(ctx context.Context, cancel context.CancelFunc, frame ProgressFrame) (*LiveProgress, error) {
    if !s.options.Rich {
        return nil, nil
    }
    if err := s.acquire(ctx, true); err != nil {
        return nil, err
    }
    runCtx, stop := context.WithCancel(ctx)
    input, closeInput := richInput(runCtx, s.options.In)
    output := &formWriter{writer: s.options.Err, cancel: cancel}
    var writer io.Writer = output
    if terminal, ok := s.options.Err.(terminalOutput); ok {
        writer = terminalFormWriter{terminalOutput: terminal, checked: output}
    }
    live := &LiveProgress{done: make(chan error, 1), writer: output}
    live.program = tea.NewProgram(progressModel{ctx: runCtx, cancel: cancel, frame: frame, width: Width(s.options.Err), dark: s.Dark(), background: s.dark.Store}, tea.WithInput(input), tea.WithOutput(writer), tea.WithoutSignalHandler(), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
        if _, quit := msg.(tea.QuitMsg); quit {
            closeInput()
        }
        if msg == tea.RequestWindowSize() {
            return nil
        }
        return msg
    }))
    go func() {
        defer stop()
        defer s.release()
        stopResize := watchRichSize(live.program, s.options.Err)
        _, err := live.program.Run()
        stopResize()
        closeInput()
        if failure := output.failure(); failure != nil {
            err = failure
        }
        live.done <- err
    }()
    return live, nil
}

// Update replaces display data without performing network operations.
func (l *LiveProgress) Update(frame ProgressFrame) error {
    l.program.Send(frame)
    return l.writer.failure()
}

// Close restores the terminal and joins its input worker before returning.
func (l *LiveProgress) Close() error {
    var err error
    l.once.Do(func() { l.program.Send(progressFinished{}); err = <-l.done })
    return err
}

type progressTick struct{}
type progressFinished struct{}
type progressCanceled struct{}
type progressModel struct {
    finished, dark bool
    width, tick    int
    background     func(bool)
    details        bool
    ctx            context.Context
    cancel         context.CancelFunc
    frame          ProgressFrame
}

func progressClock() tea.Cmd {
    return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return progressTick{} })
}
func (m progressModel) Init() tea.Cmd {
    return tea.Batch(progressClock(), func() tea.Msg { <-m.ctx.Done(); return progressCanceled{} })
}
func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch v := msg.(type) {
    case ProgressFrame:
        if v.Document.Title != m.frame.Document.Title {
            m.details = false
        }
        m.frame = v
    case progressFinished, progressCanceled:
        m.finished = true
        return m, tea.Quit
    case tea.WindowSizeMsg:
        m.width = v.Width
    case tea.BackgroundColorMsg:
        m.dark = v.IsDark()
        if m.background != nil {
            m.background(m.dark)
        }
    case progressTick:
        m.tick++
        return m, progressClock()
    case tea.KeyPressMsg:
        if v.String() == "e" && m.frame.Edit != nil {
            m.frame.Edit()
            m.finished = true
            return m, tea.Quit
        }
        if v.String() == "tab" {
            m.details = !m.details
        }
        if v.String() == "ctrl+c" {
            m.cancel()
            m.finished = true
            return m, tea.Quit
        }
    }
    return m, nil
}
func (m progressModel) View() tea.View {
    if m.finished {
        return tea.NewView("")
    }
    if m.frame.Document.Title != "" {
        d := m.frame.Document
        spinner := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
        d.Title = string(spinner[m.tick%len(spinner)]) + "  " + d.Title
        if m.details && m.frame.Detail != "" {
            d.Title = m.frame.DetailTitle
            d.Rows = nil
            d.Body = m.frame.Detail
            d.Footer = m.frame.DetailFooter
        } else {
            clock := ""
            if !m.frame.StartedAt.IsZero() {
                clock = m.frame.ElapsedLabel + " " + time.Since(m.frame.StartedAt).Round(time.Second).String()
            }
            if !m.frame.NextAt.IsZero() {
                clock += "  ·  " + m.frame.NextLabel + " " + max(time.Duration(0), time.Until(m.frame.NextAt)).Round(time.Second).String()
            }
            if clock != "" {
                if d.Body != "" {
                    d.Body += "\n"
                }
                d.Body += clock
            }
            d.Footer = m.frame.Footer
        }
        return tea.NewView(d.Render(m.width, true, m.dark) + "\n")
    }
    text := m.frame.Text
    if m.details && m.frame.Detail != "" {
        text += "\n" + m.frame.Detail
    }
    if !m.frame.StartedAt.IsZero() {
        text += fmt.Sprintf("\n%s %s", m.frame.ElapsedLabel, time.Since(m.frame.StartedAt).Round(time.Second))
    }
    if !m.frame.NextAt.IsZero() {
        text += fmt.Sprintf("\n%s %s", m.frame.NextLabel, max(time.Duration(0), time.Until(m.frame.NextAt)).Round(time.Second))
    }
    return tea.NewView(text + "\n")
}
