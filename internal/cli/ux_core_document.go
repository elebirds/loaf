package cli

import (
    "context"
    "strings"

    "charm.land/lipgloss/v2"

    "github.com/elebirds/loaf/internal/cli/interaction"
)

// uxShowDocument displays a bounded, paged detail without writing stdout or
// converting an application result into a second business representation.
func (c *invocation) uxShowDocument(ctx context.Context, doc interaction.Document) error {
    if !c.uxInteractive() {
        return interaction.ErrNonInteractive
    }
    title := doc.Title
    if title == "" {
        title = c.output.locale.Text("ux_core_details")
    }
    doc.Title = ""
    width := max(20, min(96, interaction.Width(c.options.Err))-8)
    lines := strings.Split(lipgloss.NewStyle().Width(width).Render(doc.Content(width, false, c.input.Dark())), "\n")
    size := max(3, interaction.Height(c.options.Err)-13)
    page := 0
    for {
        end := min(len(lines), (page+1)*size)
        summary := strings.Join(lines[page*size:end], "\n")
        if len(lines) > size {
            summary += "\n" + c.output.locale.Text("ux_core_page_count", map[string]any{"Count": page + 1, "Total": (len(lines) + size - 1) / size})
        }
        choices := []interaction.Choice{}
        if end < len(lines) {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_next"), Value: "next"})
        }
        if page > 0 {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_previous"), Value: "previous"})
        }
        choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_core_back"), Value: "back"})
        action, err := c.input.SelectWithSummary(ctx, title, summary, choices, "back")
        if err != nil {
            return err
        }
        switch action {
        case "next":
            page++
        case "previous":
            page--
        default:
            return nil
        }
    }
}

// uxShowResult reuses direct-command rendering inside the bounded detail view.
// A domain that already displayed its result returns nil rather than repeating it.
func (c *invocation) uxShowResult(ctx context.Context, data any, cause error) error {
    var body strings.Builder
    if data != nil {
        if err := printResult(&body, data, c.output.locale, cause == nil); err != nil {
            return err
        }
    }
    doc := interaction.Document{Title: c.output.locale.Text("ux_core_result"), Body: strings.TrimSpace(body.String())}
    if cause != nil {
        view := c.output.error(cause)
        doc.Title, doc.Tone = view.Message, "error"
        if view.Hint != "" {
            doc.Body = strings.TrimSpace(doc.Body + "\n" + view.Hint)
        }
    }
    return c.uxShowDocument(ctx, doc)
}
