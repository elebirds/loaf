package cli

import (
    "context"
    "errors"
    "fmt"
    "slices"
    "strconv"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/task"
)

func (c *invocation) uxDFilters(ctx context.Context, original client.PSInput) (client.PSInput, bool, error) {
    draft := original
    draft.Tags, draft.Statuses = slices.Clone(original.Tags), slices.Clone(original.Statuses)
    l := c.output.locale
    for {
        choices := []interaction.Choice{}
        for _, key := range []string{"name", "tags", "target", "since", "until", "limit"} {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_d_filter_" + key), Value: key})
        }
        if !draft.Local {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_d_filter_status"), Value: "status"})
        }
        choices = append(choices,
            interaction.Choice{Label: l.Text("ux_d_filter_values"), Value: "values"},
            interaction.Choice{Label: l.Text("ux_d_filter_clear"), Value: "clear"},
            interaction.Choice{Label: l.Text("ux_d_filter_apply"), Value: "apply"},
            interaction.Choice{Label: l.Text("ux_d_cancel"), Value: "cancel"})
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_d_filters"), l.Text("ux_d_filter_help"), choices, "apply")
        if err != nil || action == "cancel" {
            return original, false, err
        }
        switch action {
        case "apply":
            if err := uxDValidateFilters(draft); err != nil {
                if err := c.uxDRecover(ctx, err); err != nil {
                    return original, false, err
                }
                continue
            }
            return draft, true, nil
        case "clear":
            draft = client.PSInput{Local: original.Local, Policy: original.Policy}
        case "values":
            err = c.uxShowDocument(ctx, interaction.Document{Title: l.Text("ux_d_filter_values"), Body: c.uxDFilterSummary(draft)})
        case "tags":
            draft.Tags, err = c.uxDTags(ctx, draft.Tags)
        case "status":
            f := interaction.Field{Title: l.Text("ux_d_filter_status"), Multiple: true, Values: slices.Clone(draft.Statuses)}
            for _, status := range []string{"starting", "running", "succeeded", "failed", "stopped", "unknown"} {
                f.Choices = append(f.Choices, interaction.Choice{Label: localizedRunState(status, l), Value: status})
            }
            err = c.input.Page(ctx, l.Text("ux_d_filter_status"), &f)
            if err == nil {
                draft.Statuses = f.Values
            }
        default:
            current := ""
            var dest **string
            switch action {
            case "name":
                dest = &draft.Name
            case "target":
                dest = &draft.Target
            case "since":
                dest = &draft.Since
            case "until":
                dest = &draft.Until
            case "limit":
                if draft.Limit != nil {
                    current = strconv.Itoa(*draft.Limit)
                }
            }
            if dest != nil && *dest != nil {
                current = **dest
            }
            var text string
            text, err = c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_d_filter_" + action), Description: l.Text("ux_d_edit_help", map[string]any{"Value": uxDSafe(current)}), MaxBytes: 512})
            if err == nil {
                if action == "limit" {
                    if text == "" {
                        draft.Limit = nil
                    } else {
                        n, parseErr := strconv.Atoi(text)
                        if parseErr != nil || n < 1 || n > 1000 {
                            err = fault.Invalid("--limit", "out_of_range", "ps_invalid_limit")
                        } else {
                            draft.Limit = &n
                        }
                    }
                } else if text == "" {
                    *dest = nil
                } else {
                    *dest = &text
                }
            }
        }
        if err := c.uxDRecover(ctx, err); err != nil {
            return original, false, err
        }
    }
}

func uxDValidateFilters(in client.PSInput) error {
    f := execution.HistoryFilters{Statuses: in.Statuses, Name: deref(in.Name), Tags: in.Tags}
    for _, field := range []struct {
        raw  *string
        dest **time.Time
    }{{in.Since, &f.Since}, {in.Until, &f.Until}} {
        if field.raw != nil {
            value, err := time.Parse(time.RFC3339Nano, *field.raw)
            if err != nil {
                return fault.Invalid("--since/--until", "invalid_type", "ps_invalid_time")
            }
            *field.dest = &value
        }
    }
    if in.Target != nil && !config.ValidName(*in.Target) {
        return fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    if in.Limit != nil {
        if _, err := config.ResolvePSLimit(config.PSSettings{Limit: in.Limit}); err != nil {
            return err
        }
    }
    return f.Validate()
}

func (c *invocation) uxDTags(ctx context.Context, initial []string) ([]string, error) {
    tags := slices.Clone(initial)
    l := c.output.locale
    for {
        choices := []interaction.Choice{{Label: l.Text("ux_d_tag_add"), Value: "add"}}
        for i, tag := range tags {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_d_tag_remove", map[string]any{"Value": tag}), Value: strconv.Itoa(i)})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_core_back"), Value: "back"})
        action, err := c.input.Select(ctx, l.Text("ux_d_filter_tags"), choices, "back")
        if err != nil || action == "back" {
            return tags, err
        }
        if action == "add" {
            tag, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("ux_d_tag_add"), Description: l.Text("ux_d_tag_help"), MaxBytes: 256, Validate: func(tag string) error {
                if _, err := task.NormalizeRunMetadata(task.RunMetadata{Tags: append(slices.Clone(tags), tag)}); err != nil {
                    return errors.New(c.output.error(err).Message)
                }
                return nil
            }})
            if err != nil {
                return tags, err
            }
            if !slices.Contains(tags, tag) {
                tags = append(tags, tag)
            }
        } else {
            i, _ := strconv.Atoi(action)
            tags = slices.Delete(tags, i, i+1)
        }
    }
}

func (c *invocation) uxDFilterSummary(in client.PSInput) string {
    var rows []string
    l := c.output.locale
    for _, field := range []struct{ key, text string }{
        {"name", deref(in.Name)}, {"tags", strings.Join(in.Tags, " · ")}, {"target", deref(in.Target)},
        {"since", deref(in.Since)}, {"until", deref(in.Until)}, {"status", strings.Join(in.Statuses, ", ")},
    } {
        if field.text != "" {
            rows = append(rows, l.Text("ux_d_filter_"+field.key)+": "+uxDSafe(field.text))
        }
    }
    if in.Limit != nil {
        rows = append(rows, fmt.Sprintf("%s: %d", l.Text("ux_d_filter_limit"), *in.Limit))
    }
    if len(rows) == 0 {
        return l.Text("ux_d_filter_none")
    }
    return strings.Join(rows, "\n")
}
