package interaction

import (
    "image/color"
    "io"
    "strings"

    "charm.land/huh/v2"
    "charm.land/lipgloss/v2"
    "github.com/charmbracelet/x/ansi"
    "golang.org/x/term"
)

// Row is a label/value pair, not an application object.
type Row struct{ Label, Value string }

// Document is the common hierarchy for a transient screen or a final result.
type Document struct {
    Title, Context, Body, Footer, Tone string
    Rows                               []Row
}

type palette struct{ accent, muted, good, warning, bad color.Color }

func colors(dark bool) palette {
    if dark {
        return palette{lipgloss.Color("#5EEAD4"), lipgloss.Color("#94A3B8"), lipgloss.Color("#86EFAC"), lipgloss.Color("#FBBF24"), lipgloss.Color("#FDA4AF")}
    }
    return palette{lipgloss.Color("#0F766E"), lipgloss.Color("#64748B"), lipgloss.Color("#15803D"), lipgloss.Color("#B45309"), lipgloss.Color("#BE123C")}
}

// Render keeps the primary state, facts and next action in separate visual groups.
func (d Document) Render(width int, styled, dark bool) string {
    if width <= 0 {
        width = 80
    }
    width = max(24, min(width, 96)) - 4
    return trimLines(lipgloss.NewStyle().Padding(1, 2).Render(d.Content(width, styled, dark)))
}

// Content renders without outer padding, for a summary within a form.
func (d Document) Content(width int, styled, dark bool) string {
    width = max(20, width)
    c := colors(dark)
    title, label, footer := lipgloss.NewStyle().Bold(styled), lipgloss.NewStyle(), lipgloss.NewStyle()
    if styled {
        tone := c.accent
        switch d.Tone {
        case "success":
            tone = c.good
        case "warning":
            tone = c.warning
        case "error":
            tone = c.bad
        }
        title = title.Foreground(tone)
        label = label.Foreground(c.muted)
        footer = footer.Foreground(c.muted)
    }
    parts := []string{}
    if d.Title != "" {
        parts = append(parts, title.Width(width).Render(d.Title))
    }
    if d.Context != "" {
        parts = append(parts, label.Width(width).Render(d.Context))
    }
    if len(d.Rows) > 0 {
        labelWidth := 0
        for _, r := range d.Rows {
            if r.Value != "" {
                labelWidth = max(labelWidth, lipgloss.Width(r.Label))
            }
        }
        labelWidth = min(labelWidth, 16)
        rows := []string{}
        for _, r := range d.Rows {
            if r.Value == "" {
                continue
            }
            rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, label.Width(labelWidth+2).Render(r.Label), lipgloss.NewStyle().Width(max(8, width-labelWidth-2)).Render(r.Value)))
        }
        parts = append(parts, "", strings.Join(rows, "\n"))
    }
    if d.Body != "" {
        parts = append(parts, "", lipgloss.NewStyle().Width(width).Render(d.Body))
    }
    if d.Footer != "" {
        parts = append(parts, "", footer.Width(width).Render(d.Footer))
    }
    return trimLines(strings.Join(parts, "\n"))
}

func trimLines(value string) string {
    rendered := strings.Split(value, "\n")
    for i, line := range rendered {
        visible := strings.TrimRight(ansi.Strip(line), " ")
        rendered[i] = ansi.Truncate(line, ansi.StringWidth(visible), "")
    }
    return strings.Join(rendered, "\n")
}

// Width observes the caller's terminal without opening a replacement tty.
func Width(out io.Writer) int {
    if fd, ok := out.(interface{ Fd() uintptr }); ok {
        if w, _, err := term.GetSize(int(fd.Fd())); err == nil && w > 0 {
            return w
        }
    }
    return 80
}

// Height reports terminal rows, with a conventional fallback for plain output.
func Height(out io.Writer) int {
    if fd, ok := out.(interface{ Fd() uintptr }); ok {
        if _, h, err := term.GetSize(int(fd.Fd())); err == nil && h > 0 {
            return h
        }
    }
    return 24
}

// Rich reports the session's effective terminal capability, not just a flag.
func (s *InputSession) Rich() bool { return s.options.Rich }
func (s *InputSession) Dark() bool { return s.dark.Load() }

func formTheme(dark bool) *huh.Styles {
    t := huh.ThemeBase(dark)
    c := colors(dark)
    t.Form.Base = lipgloss.NewStyle().Padding(1, 2)
    t.Group.Title = lipgloss.NewStyle().Bold(true).Foreground(c.accent).MarginBottom(1)
    t.Group.Description = lipgloss.NewStyle().MarginBottom(1)
    t.Focused.Base = t.Focused.Base.BorderForeground(c.accent)
    t.Focused.Title = lipgloss.NewStyle().Bold(true)
    t.Focused.Description = lipgloss.NewStyle().Foreground(c.muted)
    t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(c.accent).SetString("› ")
    t.Focused.MultiSelectSelector = t.Focused.SelectSelector
    t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(c.accent).SetString("✓ ")
    t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(c.muted).SetString("○ ")
    t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(c.accent)
    t.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(c.bad)
    t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(c.accent)
    t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(c.accent)
    t.Blurred = t.Focused
    t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
    t.Blurred.Title = lipgloss.NewStyle().Foreground(c.muted)
    t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
    t.Blurred.MultiSelectSelector = t.Blurred.SelectSelector
    return t
}
