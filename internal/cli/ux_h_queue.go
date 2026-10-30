package cli

import (
    "context"
    "errors"
    "slices"
    "strconv"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/queue"
)

// uxHInputs consumes the machine and command editors without owning either
// workflow. Production entries bind the delivered A/C methods explicitly.
type uxHInputs struct {
    pickTarget  func(context.Context, string) (string, error)
    editCommand func(context.Context, []string) ([]string, error)
}

func (c *invocation) uxHQueue(ctx context.Context) (any, error) {
    return c.uxHQueueWith(ctx, uxHInputs{pickTarget: c.uxAPickTarget, editCommand: c.uxCEditCommand})
}

func (c *invocation) uxHReady() error {
    if !c.uxInteractive() || c.output.json {
        return interaction.ErrNonInteractive
    }
    if c.app == nil || c.app.Queue == nil {
        return asyncUnavailable("queue")
    }
    return nil
}

// uxHSelection reads the current project preference once. Menu selection never
// writes a default, and subsequent queue calls carry this explicit alias.
func (c *invocation) uxHSelection(ctx context.Context) (target, project string, err error) {
    if c.app.Machines == nil {
        return "", "", asyncUnavailable("machine")
    }
    choices, err := c.app.Machines.Targets(ctx, "")
    if err != nil {
        return "", "", err
    }
    target = choices.Project.Target
    if c.grammar.Target != nil {
        target = *c.grammar.Target
    }
    return target, choices.Project.ProjectID, nil
}

// Queue operations may invoke native SSH authentication. No form or live
// progress owns stdin while the service runs; progress callbacks are sparse.
func (c *invocation) uxHCall(ctx context.Context, call func() (queue.Result, error)) (result queue.Result, err error) {
    err = c.input.WithNative(ctx, nil, func() error {
        var callErr error
        result, callErr = call()
        return callErr
    })
    return result, err
}

func (c *invocation) uxHQueueWith(ctx context.Context, inputs uxHInputs) (any, error) {
    if err := c.uxHReady(); err != nil {
        return nil, err
    }
    target, project, err := c.uxHSelection(ctx)
    if err != nil {
        return nil, err
    }
    limit := c.grammar.Queue.List.Limit
    if err := boundedAsyncLimit(limit); err != nil {
        return nil, err
    }
    var result queue.Result
    var queryErr error
    refresh := func() {
        result, queryErr = c.uxHCall(ctx, func() (queue.Result, error) {
            return c.app.Queue.List(ctx, client.QueueListInput{Target: &target, Limit: limit, Policy: c.policy})
        })
    }
    refresh()
    selected, search, page := "", "", 0
    for {
        if ctx.Err() != nil {
            return nil, ctx.Err()
        }
        choices := []interaction.Choice{}
        if len(result.Requests) > 0 {
            choices = append(choices, c.uxHChoice("requests"))
        }
        choices = append(choices, c.uxHChoice("refresh"), c.uxHChoice("submit"), c.uxHChoice("manage"))
        if inputs.pickTarget != nil {
            choices = append(choices, c.uxHChoice("machine"))
        }
        choices = append(choices, c.uxHChoice("back"))
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_h_title"), c.uxHSummary(target, result, queryErr, limit), choices, "")
        if errors.Is(err, interaction.ErrCanceled) {
            return nil, nil
        }
        if err != nil || action == "back" {
            return nil, err
        }
        switch action {
        case "refresh":
            refresh()
        case "requests":
            err = c.uxHRequests(ctx, target, project, &result, &queryErr, &limit, &selected, &search, &page)
        case "machine":
            var next string
            next, err = inputs.pickTarget(ctx, target)
            if err == nil && next != "" && next != target {
                target, selected, search, page = next, "", "", 0
                refresh()
            }
        case "submit":
            child, childErr := c.uxChild("queue", "submit")
            if childErr != nil {
                return nil, childErr
            }
            child.grammar.Target = &target
            _, err = child.uxHSubmitWith(ctx, inputs)
            if err == nil {
                _, project, err = c.uxHSelection(ctx)
                if err == nil {
                    refresh()
                }
            }
        case "manage":
            var changed bool
            changed, err = c.uxHManage(ctx, target, result, queryErr)
            if changed && err == nil {
                refresh()
            }
        }
        if err != nil && !errors.Is(err, interaction.ErrCanceled) {
            return nil, err
        }
    }
}

func (c *invocation) uxHRequests(ctx context.Context, target, project string, result *queue.Result, queryErr *error, limit *int, selected, search *string, page *int) error {
    for {
        matches := uxHFilter(result.Requests, *search)
        // Explicit pages also bound the numbered plain-terminal counterpart.
        const pageSize = 4
        *page = min(*page, max(0, (len(matches)-1)/pageSize))
        start, end := *page*pageSize, min(len(matches), (*page+1)*pageSize)
        choices := []interaction.Choice{}
        for _, row := range matches[start:end] {
            label := c.uxHLine(row)
            choices = append(choices, interaction.Choice{Label: label, Value: "request:" + row.ID})
        }
        if end < len(matches) {
            choices = append(choices, c.uxHChoice("next"))
        }
        if start > 0 {
            choices = append(choices, c.uxHChoice("previous"))
        }
        choices = append(choices, c.uxHChoice("search"), c.uxHChoice("refresh"), c.uxHChoice("limit"), c.uxHChoice("back"))
        summary := c.output.locale.Text("ux_h_page", map[string]any{"Start": min(start+1, len(matches)), "End": end, "Total": len(matches), "Count": len(result.Requests), "Limit": *limit})
        if *search != "" {
            summary += "\n" + c.output.locale.Text("ux_h_filter", map[string]any{"Value": uxHText(*search)})
        }
        if result.HasMore {
            summary += "\n" + c.output.locale.Text("ux_h_more")
        }
        if *queryErr != nil || result.StateError != "" {
            summary += "\n" + c.uxHError(*queryErr, result.StateError)
        }
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_h_requests"), summary, choices, *selected)
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "next":
            *page++
            *selected = ""
        case "previous":
            *page--
            *selected = ""
        case "search":
            value, err := c.input.Text(ctx, interaction.TextPrompt{Title: c.output.locale.Text("ux_h_search_prompt"), MaxBytes: 256})
            if err != nil {
                return err
            }
            *search, *selected, *page = value, "", 0
        case "limit":
            value, err := c.uxHNumber(ctx, "ux_h_limit", *limit, 1000)
            if err != nil {
                return err
            }
            *limit = value
            fallthrough
        case "refresh":
            *result, *queryErr = c.uxHCall(ctx, func() (queue.Result, error) {
                return c.app.Queue.List(ctx, client.QueueListInput{Target: &target, Limit: *limit, Policy: c.policy})
            })
        default:
            *selected = action
            id := strings.TrimPrefix(action, "request:")
            for _, row := range matches[start:end] {
                if row.ID == id {
                    if err := c.uxHRequest(ctx, row, result.Status, project != "" && row.ProjectID == project); err != nil && !errors.Is(err, interaction.ErrCanceled) {
                        return err
                    }
                    break
                }
            }
        }
    }
}

func (c *invocation) uxHRequest(ctx context.Context, seed queue.Request, status *queue.Status, local bool) error {
    result := queue.Result{Request: &seed, Status: status}
    var queryErr error
    fresh := false
    refresh := func() {
        observed, err := c.uxHCall(ctx, func() (queue.Result, error) {
            return c.app.Queue.Show(ctx, client.QueueIDInput{ID: seed.ID, Policy: c.policy})
        })
        // Keep an earlier fact visible, but never use it as fresh eligibility.
        fresh = observed.Request != nil && (err == nil || observed.StateError != "")
        if observed.Request != nil {
            result = observed
        } else {
            result.StateError = observed.StateError
        }
        queryErr = err
    }
    if local {
        refresh()
    }
    for {
        row := *result.Request
        choices := []interaction.Choice{c.uxHChoice("details")}
        if local {
            choices = append(choices, c.uxHChoice("refresh"))
        }
        if fresh && uxHCancelable(row.State) || local && row.State == "preparing" && uxHCode(queryErr) == "QUEUE_NOT_FOUND" {
            choices = append(choices, c.uxHChoice("cancel"))
        }
        if fresh && row.State == "canceled" {
            choices = append(choices, c.uxHChoice("clean"))
        }
        for _, route := range c.uxHRunRoutes(row, local) {
            choices = append(choices, c.uxHChoice(route.Kind))
        }
        choices = append(choices, c.uxHChoice("back"))
        summary := c.uxHLine(row) + "\n" + c.uxHReason(row, result.Status)
        if !local {
            summary += "\n" + c.output.locale.Text("ux_h_foreign")
        }
        if queryErr != nil || result.StateError != "" {
            summary += "\n" + c.uxHError(queryErr, result.StateError)
        }
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_h_request"), summary, choices, "")
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "details":
            err = c.uxShowDocument(ctx, c.uxHRequestDocument(result, queryErr))
        case "refresh":
            refresh()
        case "cancel", "clean":
            var changed queue.Result
            var attempted bool
            changed, attempted, err = c.uxHMutateRequest(ctx, row, action)
            if attempted {
                if changed.Request != nil {
                    result = changed
                }
                fresh = false // Only a new explicit observation enables another mutation.
                queryErr = err
                err = c.uxHShowOutcome(ctx, changed, err)
            }
        default:
            for _, route := range c.uxHRunRoutes(row, local) {
                if route.Kind == action {
                    data, cause := c.uxNavigate(ctx, route)
                    if data != nil || cause != nil {
                        err = c.uxShowResult(ctx, data, cause)
                    }
                    break
                }
            }
        }
        if err != nil {
            return err
        }
    }
}

func (c *invocation) uxHMutateRequest(ctx context.Context, row queue.Request, action string) (queue.Result, bool, error) {
    if action != "cancel" && action != "clean" || action == "cancel" && !uxHCancelable(row.State) || action == "clean" && row.State != "canceled" {
        return queue.Result{}, false, fault.New("QUEUE_NOT_CANCELABLE", "queue_operation_failed", map[string]any{"Code": "QUEUE_NOT_CANCELABLE"})
    }
    if action == "clean" {
        preview, err := c.uxHCall(ctx, func() (queue.Result, error) {
            return c.app.Queue.Clean(ctx, client.QueueCleanInput{IDs: []string{row.ID}, DryRun: true, Policy: c.policy})
        })
        if err != nil {
            return preview, true, err
        }
        if err := c.uxShowResult(ctx, queueCleanResult{DryRun: true, IDs: []string{row.ID}, Result: preview}, nil); err != nil {
            return preview, false, err
        }
    }
    yes, err := c.uxHConfirm(ctx, "ux_h_"+action+"_confirm", row.ID)
    if err != nil || !yes {
        return queue.Result{}, false, err
    }
    result, err := c.uxHCall(ctx, func() (queue.Result, error) {
        if action == "clean" {
            return c.app.Queue.Clean(ctx, client.QueueCleanInput{IDs: []string{row.ID}, Policy: c.policy})
        }
        return c.app.Queue.Cancel(ctx, client.QueueIDInput{ID: row.ID, Policy: c.policy})
    })
    // A response without the durable canceled state is not cancellation proof.
    if action == "cancel" && err == nil && (result.Request == nil || result.Request.ID != row.ID || result.Request.State != "canceled") {
        err = fault.New("QUEUE_CANCEL_UNCONFIRMED", "queue_operation_failed", map[string]any{"Code": "QUEUE_CANCEL_UNCONFIRMED"})
    }
    if action == "clean" && err == nil && !slices.Contains(result.Cleaned, row.ID) {
        err = fault.New("QUEUE_CLEAN_UNCONFIRMED", "queue_operation_failed", map[string]any{"Code": "QUEUE_CLEAN_UNCONFIRMED"})
    }
    return result, true, err
}

func (c *invocation) uxHManage(ctx context.Context, target string, result queue.Result, cause error) (bool, error) {
    choices := []interaction.Choice{c.uxHChoice("details")}
    if cause == nil && result.Status != nil {
        action := "pause"
        if result.Status.Paused {
            action = "resume"
        }
        choices = append(choices, c.uxHChoice(action), c.uxHChoice("capacity"))
    } else if uxHCode(cause) == "QUEUE_NOT_ENABLED" {
        choices = append(choices, c.uxHChoice("enable"))
    }
    choices = append(choices, c.uxHChoice("back"))
    action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_h_manage"), c.output.locale.Text("ux_h_scope", map[string]any{"Target": uxHText(target)}), choices, "back")
    if err != nil || action == "back" {
        return false, err
    }
    if action == "details" {
        return false, c.uxShowResult(ctx, result, cause)
    }
    slots := 1
    if result.Status != nil {
        slots = result.Status.Slots
    }
    if action == "enable" || action == "capacity" {
        slots, err = c.uxHNumber(ctx, "ux_h_slots", slots, 64)
        if err != nil {
            return false, err
        }
    }
    scope := c.output.locale.Text("ux_h_scope", map[string]any{"Target": uxHText(target)})
    if action == "enable" || action == "capacity" {
        scope += "\n" + c.output.locale.Text("ux_h_enable_scope", map[string]any{"Count": slots})
    }
    yes, err := c.uxHConfirm(ctx, "ux_h_"+action+"_confirm", scope)
    if err != nil || !yes {
        return false, err
    }
    result, err = c.uxHCall(ctx, func() (queue.Result, error) {
        if action == "enable" || action == "capacity" {
            return c.app.Queue.Enable(ctx, client.QueueEnableInput{Target: &target, Slots: slots, Policy: c.policy})
        }
        return c.app.Queue.SetPaused(ctx, client.QueuePauseInput{Target: &target, Paused: action == "pause", Policy: c.policy})
    })
    return true, c.uxHShowOutcome(ctx, result, err)
}

func (c *invocation) uxHConfirm(ctx context.Context, message, value string) (bool, error) {
    return c.input.Confirm(ctx, interaction.Confirmation{Title: c.output.locale.Text(message, map[string]any{"Value": value}), Kind: interaction.ConfirmDefaultNo, AcceptLabel: c.output.locale.Text("yes"), RejectLabel: c.output.locale.Text("no")})
}

func (c *invocation) uxHNumber(ctx context.Context, title string, initial, maximum int) (int, error) {
    field := &interaction.Field{Title: c.output.locale.Text(title), Value: strconv.Itoa(initial), Validate: func(value string) error {
        n, err := strconv.Atoi(value)
        if err != nil || n < 1 || n > maximum {
            return errors.New(c.output.locale.Text("ux_h_number", map[string]any{"Count": maximum}))
        }
        return nil
    }}
    if err := c.input.Page(ctx, c.output.locale.Text("ux_h_title"), field); err != nil {
        return 0, err
    }
    return strconv.Atoi(field.Value)
}

// Preserve the last verified facts even when cancellation prevents opening a
// result form. The parent hub otherwise exits immediately on canceled context.
func (c *invocation) uxHShowOutcome(ctx context.Context, data any, cause error) error {
    if ctx.Err() != nil {
        writeErr := printResult(c.output.err, data, c.output.locale, cause == nil)
        return errors.Join(cause, ctx.Err(), writeErr)
    }
    return c.uxShowResult(ctx, data, cause)
}
