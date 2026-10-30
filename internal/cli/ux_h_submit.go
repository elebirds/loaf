package cli

import (
    "context"
    "encoding/json"
    "errors"
    "slices"
    "strings"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *invocation) uxHSubmit(ctx context.Context) (any, error) {
    return c.uxHSubmitWith(ctx, uxHInputs{pickTarget: c.uxAPickTarget, editCommand: c.uxCEditCommand})
}

func (c *invocation) uxHSubmitWith(ctx context.Context, inputs uxHInputs) (any, error) {
    if err := c.uxHReady(); err != nil {
        return nil, err
    }
    target, _, err := c.uxHSelection(ctx)
    if err != nil {
        return nil, err
    }
    v := c.grammar.Queue.Submit
    args, err := commandArgs(v.Argv, false)
    if err != nil {
        return nil, err
    }
    if len(v.Argv) > 0 && len(args) == 0 {
        return nil, fault.Invalid("command", "missing_argument", "run_command")
    }
    draft := client.QueueSubmitInput{Target: &target, Args: slices.Clone(args), Name: v.Name, Tags: slices.Clone(v.Tag), Notify: slices.Clone(v.Notify), Policy: c.policy}
    for {
        choices := []interaction.Choice{c.uxHChoice("review")}
        if inputs.editCommand != nil {
            choices = append(choices, c.uxHChoice("command"))
        }
        if inputs.pickTarget != nil {
            choices = append(choices, c.uxHChoice("machine"))
        }
        choices = append(choices, c.uxHChoice("labels"), c.uxHChoice("notifications"), c.uxHChoice("back"))
        command := c.output.locale.Text("ux_h_project_command")
        if draft.Args != nil {
            command = uxHQuoted(draft.Args)
        }
        summary := c.output.locale.Text("ux_h_target", map[string]any{"Target": uxHText(target)}) + "\n" + ansi.Truncate(command, max(20, min(80, interaction.Width(c.options.Err))-12), "…")
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_h_submit"), summary, choices, "")
        if err != nil || action == "back" {
            return nil, err
        }
        switch action {
        case "command":
            // The shared editor owns argv parsing and nil/project semantics.
            edited, editErr := inputs.editCommand(ctx, slices.Clone(draft.Args))
            if editErr == nil {
                if edited != nil && len(edited) == 0 {
                    editErr = fault.Invalid("command", "missing_argument", "run_command")
                } else {
                    draft.Args = slices.Clone(edited)
                }
            }
            err = editErr
        case "machine":
            var next string
            next, err = inputs.pickTarget(ctx, target)
            if err == nil && next != "" {
                target = next
            }
        case "labels":
            name, fieldErr := c.input.Text(ctx, interaction.TextPrompt{Title: c.output.locale.Text("ux_h_name_prompt"), MaxBytes: 512})
            if fieldErr != nil {
                err = fieldErr
                break
            }
            tags, fieldErr := c.input.Text(ctx, interaction.TextPrompt{Title: c.output.locale.Text("ux_h_tags_prompt"), MaxBytes: 4096})
            if fieldErr != nil {
                err = fieldErr
                break
            }
            var values []string
            if strings.TrimSpace(tags) != "" {
                if json.Unmarshal([]byte(tags), &values) != nil || values == nil {
                    err = c.uxShowResult(ctx, nil, fault.Invalid("--tag", "invalid_value", "ux_h_tags_invalid"))
                    break
                }
            }
            draft.Name, draft.Tags = name, values
        case "notifications":
            err = c.uxHNotifications(ctx, &draft)
        case "review":
            previewInput := draft
            previewInput.DryRun = true
            // Do not probe queue status/credentials or acquire native ownership
            // for a local plan. The existing application planner validates all
            // command, metadata, source, output and environment rules.
            preview, cause := c.app.Queue.Submit(ctx, previewInput, nil)
            if cause != nil {
                err = c.uxShowResult(ctx, preview, cause)
                break
            }
            if preview.Preview == nil {
                return nil, fault.New("QUEUE_RESPONSE_MISMATCH", "queue_operation_failed", map[string]any{"Code": "QUEUE_RESPONSE_MISMATCH"})
            }
            err = c.uxHReview(ctx, preview)
            if err != nil {
                break
            }
            if v.DryRun {
                return nil, nil
            }
            yes, confirmErr := c.uxHConfirm(ctx, "ux_h_submit_confirm", uxHText(target))
            if confirmErr != nil {
                err = confirmErr
                break
            }
            if !yes {
                continue
            }
            var result client.QueueSubmitResult
            cause = c.input.WithNative(ctx, nil, func() error {
                var callErr error
                result, callErr = c.app.Queue.Submit(ctx, draft, c.progress)
                return callErr
            })
            if err := c.uxHShowOutcome(ctx, result, cause); err != nil {
                return nil, err
            }
            // Never loop back to submission after a real attempt. Inspection
            // uses only the returned saved request/Run, even after lost ACKs.
            if result.Request != nil && result.NextAction != "repair_local_state" {
                return nil, c.uxHRequest(ctx, *result.Request, nil, true)
            }
            return nil, nil
        }
        if errors.Is(err, interaction.ErrCanceled) {
            continue
        }
        if err != nil {
            return nil, err
        }
    }
}

func (c *invocation) uxHReview(ctx context.Context, result client.QueueSubmitResult) error {
    var body strings.Builder
    if err := uxHPrintQueuePreview(&body, *result.Preview, c.output.locale); err != nil {
        return err
    }
    body.WriteString("\n" + c.output.locale.Text("ux_h_submit_scope"))
    return c.uxShowDocument(ctx, interaction.Document{Title: c.output.locale.Text("ux_h_review"), Body: body.String()})
}

func (c *invocation) uxHNotifications(ctx context.Context, draft *client.QueueSubmitInput) error {
    if c.app.Notify == nil {
        return c.uxShowResult(ctx, nil, asyncUnavailable("notify"))
    }
    destinations, err := c.app.Notify.Destinations(ctx)
    if err != nil {
        return c.uxShowResult(ctx, nil, err)
    }
    if len(destinations) == 0 {
        return c.uxShowDocument(ctx, interaction.Document{Title: c.output.locale.Text("ux_h_notifications"), Body: c.output.locale.Text("notify_destinations_empty")})
    }
    choices := []interaction.Choice{}
    available := map[string]bool{}
    for _, destination := range destinations {
        choices = append(choices, interaction.Choice{Label: uxHText(destination.Name), Value: destination.Name})
        available[destination.Name] = true
    }
    for _, name := range draft.Notify {
        if !available[name] {
            choices = append(choices, interaction.Choice{Label: c.output.locale.Text("ux_h_destination_missing", map[string]any{"Value": uxHText(name)}), Value: name})
            available[name] = true
        }
    }
    field := &interaction.Field{Title: c.output.locale.Text("ux_h_notifications"), Choices: choices, Multiple: true, Values: slices.Clone(draft.Notify)}
    if err := c.input.Page(ctx, c.output.locale.Text("ux_h_submit"), field); err != nil {
        return err
    }
    draft.Notify = slices.Clone(field.Values)
    return nil
}
