package interaction

import (
    "context"
    "errors"
    "fmt"
    "strconv"
    "strings"
    "sync/atomic"

    "charm.land/huh/v2"
    "charm.land/lipgloss/v2"
)

// Choice is a display label paired with an opaque application value.
type Choice struct{ Label, Value string }

// Field holds one page's input. Validation is local and never performs network I/O.
type Field struct {
    Title, Description, Value string
    Choices                   []Choice
    Values                    []string
    Multiple                  bool
    Validate                  func(string) error
    ValidateValues            func([]string) error
    Detail                    func(string) string
}

// Page collects a coherent set of fields under the command's single input owner.
func (s *InputSession) Page(ctx context.Context, title string, fields ...*Field) error {
    return s.PageWithSummary(ctx, title, "", fields...)
}

// PageWithSummary keeps the page context inside the same transient form.
func (s *InputSession) PageWithSummary(ctx context.Context, title, summary string, fields ...*Field) error {
    if err := s.acquire(ctx, true); err != nil {
        return err
    }
    defer s.release()
    for _, f := range fields {
        if len(f.Choices) == 0 {
            continue
        }
        available := map[string]bool{}
        for _, c := range f.Choices {
            available[c.Value] = true
        }
        if !f.Multiple && !available[f.Value] {
            f.Value = f.Choices[0].Value
        }
        kept := []string{}
        seen := map[string]bool{}
        for _, v := range f.Values {
            if available[v] && !seen[v] {
                kept = append(kept, v)
                seen[v] = true
            }
        }
        f.Values = kept
    }
    if s.options.Rich {
        return s.runForm(ctx, s.pageForm(title, summary, fields...), title, summary)
    }
    if err := writeText(s.options.Err, title+"\n"); err != nil {
        return err
    }
    if summary != "" {
        if err := writeText(s.options.Err, summary+"\n\n"); err != nil {
            return err
        }
    }
    for _, f := range fields {
        if len(f.Choices) == 0 {
            title := f.Title
            if f.Value != "" {
                title += " [" + f.Value + "]"
            }
            validate := func(v string) error {
                if v == "" {
                    v = f.Value
                }
                if f.Validate != nil {
                    return f.Validate(v)
                }
                return nil
            }
            v, err := s.textOwned(ctx, TextPrompt{Title: title + ": ", Description: f.Description, Validate: validate}, false)
            if err != nil {
                return err
            }
            if v != "" || f.Value == "" {
                f.Value = v
            }
            continue
        }
        if err := writeText(s.options.Err, f.Title+"\n"); err != nil {
            return err
        }
        for i, c := range f.Choices {
            mark := " "
            if !f.Multiple && c.Value == f.Value {
                mark = "*"
            }
            for _, v := range f.Values {
                if v == c.Value {
                    mark = "*"
                }
            }
            if err := writeText(s.options.Err, fmt.Sprintf("%s %d. %s\n", mark, i+1, c.Label)); err != nil {
                return err
            }
        }
        description := f.Description
        if f.Multiple {
            description = strings.TrimSpace(description + "\n" + s.options.PlainMultiHelp)
        }
        answer, err := s.textOwned(ctx, TextPrompt{Title: "> ", Description: description, Validate: func(v string) error {
            if f.Multiple && f.ValidateValues != nil {
                selected := f.Values
                if v != "" {
                    selected = []string{}
                }
                if v != "" && v != "0" {
                    for _, part := range strings.Split(v, ",") {
                        n, e := strconv.Atoi(strings.TrimSpace(part))
                        if e == nil && n >= 1 && n <= len(f.Choices) {
                            selected = append(selected, f.Choices[n-1].Value)
                        }
                    }
                }
                if err := f.ValidateValues(selected); err != nil {
                    return err
                }
            }
            if v == "" || f.Multiple && v == "0" {
                return nil
            }
            parts := strings.Split(v, ",")
            if !f.Multiple && len(parts) > 1 {
                if s.options.ChoiceError != "" {
                    return errors.New(s.options.ChoiceError)
                }
                return ErrInvalidChoice
            }
            for _, part := range parts {
                n, e := strconv.Atoi(strings.TrimSpace(part))
                if e != nil || n < 1 || n > len(f.Choices) {
                    if s.options.ChoiceError != "" {
                        return errors.New(s.options.ChoiceError)
                    }
                    return ErrInvalidChoice
                }
            }
            return nil
        }}, false)
        if err != nil {
            return err
        }
        if answer == "" {
            if f.Detail != nil {
                if err := writeText(s.options.Err, f.Detail(f.Value)+"\n"); err != nil {
                    return err
                }
            }
            continue
        }
        if f.Multiple && answer == "0" {
            f.Values = nil
            continue
        }
        if f.Multiple {
            f.Values = nil
        }
        for _, part := range strings.Split(answer, ",") {
            n, _ := strconv.Atoi(strings.TrimSpace(part))
            if f.Multiple {
                f.Values = append(f.Values, f.Choices[n-1].Value)
            } else {
                f.Value = f.Choices[n-1].Value
            }
        }
        if f.Detail != nil {
            if err := writeText(s.options.Err, f.Detail(f.Value)+"\n"); err != nil {
                return err
            }
        }
    }
    return nil
}

// pageForm builds the same fields used by the terminal and render checks.
func (s *InputSession) pageForm(title, summary string, fields ...*Field) *huh.Form {
    columns := Width(s.options.Err)
    descriptionWidth := max(12, min(columns, 96)-8)
    formFields := make([]huh.Field, 0, len(fields))
    for _, f := range fields {
        if len(f.Choices) > 0 {
            options := make([]huh.Option[string], 0, len(f.Choices))
            for _, c := range f.Choices {
                options = append(options, huh.NewOption(c.Label, c.Value))
            }
            if f.Multiple {
                description := strings.TrimSpace(f.Description + "\n" + s.options.MultiHelp)
                if s.options.Compact {
                    description = f.Description
                }
                field := huh.NewMultiSelect[string]().Title(f.Title).Description(description).Options(options...).Height(choiceHeight(f, description, columns)).Value(&f.Values)
                if f.ValidateValues != nil {
                    field.Validate(f.ValidateValues)
                }
                formFields = append(formFields, field)
            } else {
                description := strings.TrimSpace(f.Description + "\n" + s.options.SelectHelp)
                if s.options.Compact {
                    description = f.Description
                }
                heightDescription := description
                if f.Detail != nil {
                    for _, choice := range f.Choices {
                        text := f.Detail(choice.Value)
                        if !s.options.Compact {
                            text += "\n" + s.options.SelectHelp
                        }
                        if wrappedHeight(text, descriptionWidth) > wrappedHeight(heightDescription, descriptionWidth) {
                            heightDescription = text
                        }
                    }
                }
                field := huh.NewSelect[string]().Title(f.Title).Description(description).Options(options...).Height(choiceHeight(f, heightDescription, columns)).Value(&f.Value)
                if f.Detail != nil {
                    detail := &selectionDetail{target: &f.Value, describe: f.Detail}
                    detail.Set(f.Value)
                    field.Accessor(detail)
                    // Huh evaluates descriptions on a worker. Publish an
                    // immutable snapshot; never read a live form value there.
                    field.DescriptionFunc(func() string {
                        value := detail.text.Load().(string)
                        if !s.options.Compact {
                            value += "\n" + s.options.SelectHelp
                        }
                        return value
                    }, &f.Value)
                }
                formFields = append(formFields, field)
            }
        } else {
            input := huh.NewInput().Title(f.Title).Description(f.Description).Value(&f.Value).CharLimit(4096)
            if f.Validate != nil {
                input.Validate(f.Validate)
            }
            formFields = append(formFields, input)
        }
    }
    return huh.NewForm(huh.NewGroup(formFields...).Title(title).Description(summary))
}

// Huh's Height is the whole field, not the number of visible options. Styled
// empty titles also consume a viewport row. Reserve headers separately so short
// menus remain fully visible; long catalogs keep a bounded, scrollable viewport.
func choiceHeight(field *Field, description string, columns int) int {
    width := max(12, min(columns, 96)-8)
    header := max(1, wrappedHeight(field.Title, width))
    if description != "" {
        header += wrappedHeight(description, width)
    }
    rows := 0
    for _, choice := range field.Choices {
        rows += wrappedHeight(choice.Label, max(8, width-4))
        if rows >= 7 {
            break
        }
    }
    return header + min(7, rows)
}

func wrappedHeight(text string, width int) int {
    return lipgloss.Height(lipgloss.NewStyle().Width(width).Render(text))
}

// Select offers a searchable single choice with a plain-terminal counterpart.
func (s *InputSession) Select(ctx context.Context, title string, choices []Choice, value string) (string, error) {
    return s.SelectWithSummary(ctx, title, "", choices, value)
}

func (s *InputSession) SelectWithSummary(ctx context.Context, title, summary string, choices []Choice, value string) (string, error) {
    if len(choices) == 0 {
        return "", ErrInvalidChoice
    }
    f := &Field{Choices: choices, Value: value}
    err := s.PageWithSummary(ctx, title, summary, f)
    return f.Value, err
}

var ErrInvalidChoice = fmt.Errorf("select an available numbered option")

type selectionDetail struct {
    target   *string
    describe func(string) string
    text     atomic.Value
}

func (a *selectionDetail) Get() string      { return *a.target }
func (a *selectionDetail) Set(value string) { *a.target = value; a.text.Store(a.describe(value)) }
