package cli

import (
    "context"
    "errors"
    "fmt"
    "slices"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/wire"
)

func (c *invocation) uxDTask(ctx context.Context, id string) (any, error) {
    return nil, c.uxDTaskScreen(ctx, id, nil)
}

type uxDTaskState struct {
    saved        execution.LocalDetails
    latest       *execution.View
    verified     *execution.View
    cause        error
    stateFailure string
}

func (s *uxDTaskState) accept(v execution.View, cause error) {
    if v.ID != s.saved.ID {
        return
    }
    v.Log = nil
    s.latest, s.cause = &v, cause
    if v.StateError != "" {
        s.stateFailure = v.StateError
    }
    if !v.ObservedAt.IsZero() || wire.Terminal(v.Status) && v.QueryError == "" {
        s.verified = &v
    }
}

func (s uxDTaskState) view() execution.View {
    if s.verified != nil {
        return *s.verified
    }
    if s.latest != nil {
        return *s.latest
    }
    return s.saved.View
}

func (c *invocation) uxDTaskScreen(ctx context.Context, id string, seed *execution.View) error {
    if !c.uxInteractive() || c.output.json {
        return interaction.ErrNonInteractive
    }
    if id == "" {
        return fault.Invalid("run-id", "missing_argument", "ux_core_run_required")
    }
    saved, err := c.app.Projects.Inspect(ctx, id)
    if err != nil {
        return err
    }
    state := uxDTaskState{saved: saved}
    if seed != nil {
        state.accept(*seed, nil)
    }
    l := c.output.locale
    focus := "details"
    for {
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_d_task"), c.uxDTaskSummary(state), c.uxDTaskChoices(state), focus)
        if errors.Is(err, interaction.ErrCanceled) || action == "back" {
            return nil
        }
        if err != nil {
            return err
        }
        focus = action
        switch action {
        case "details":
            err = c.uxShowDocument(ctx, c.uxDTaskDocument(state))
        case "refresh":
            var result client.PSResult
            queryErr := c.input.WithNative(ctx, nil, func() error {
                var e error
                result, e = c.app.Projects.PS(ctx, client.PSInput{ID: id, Policy: c.policy})
                return e
            })
            if result.View != nil {
                state.accept(*result.View, queryErr)
            } else {
                state.cause = queryErr
            }
        case "wait":
            var result execution.WaitResult
            result, err = c.uxDWait(ctx, []string{id})
            for _, v := range result.Runs {
                state.accept(v, nil)
            }
        case "stop":
            var v *execution.View
            v, err = c.uxDStop(ctx, id)
            if v != nil {
                state.accept(*v, err)
                if ctx.Err() == nil {
                    err = c.uxShowDocument(ctx, c.uxDStopResult(*v, err))
                }
            }
        case "logs", "outputs", "clean", "notify-run":
            route := uxDTaskRoute(action, id)
            var data any
            data, err = c.uxNavigate(ctx, route)
            if !errors.Is(err, interaction.ErrCanceled) && (data != nil || err != nil) && ctx.Err() == nil {
                err = c.uxShowResult(ctx, data, err)
            }
        }
        if ctx.Err() != nil {
            return ctx.Err()
        }
        if err := c.uxDRecover(ctx, err); err != nil {
            return err
        }
        // Refresh only local facts after an explicit operation; opening or
        // returning from this screen never causes another remote observation.
        if action != "details" {
            next, readErr := c.app.Projects.Inspect(ctx, id)
            if readErr == nil {
                state.saved = next
            } else if err := c.uxDRecover(ctx, readErr); err != nil {
                return err
            }
        }
    }
}

func uxDTaskRoute(kind, id string) uxRoute {
    if kind == "clean" {
        return uxRoute{Kind: kind, IDs: []string{id}}
    }
    return uxRoute{Kind: kind, ID: id}
}

func (c *invocation) uxDTaskChoices(s uxDTaskState) []interaction.Choice {
    l := c.output.locale
    choices := []interaction.Choice{{Label: l.Text("ux_d_details"), Value: "details"}}
    v := s.view()
    if s.saved.Compatible && s.saved.CleanedAt == nil {
        choices = append(choices, interaction.Choice{Label: l.Text("ux_d_refresh"), Value: "refresh"})
        if !s.saved.CleanupPending {
            for _, entry := range []struct{ kind, label string }{{"logs", "ux_d_logs"}, {"outputs", "ux_d_outputs"}} {
                if c.uxRouteAvailable(entry.kind) {
                    choices = append(choices, interaction.Choice{Label: l.Text(entry.label), Value: entry.kind})
                }
            }
            if s.saved.Submission != "preparing" {
                choices = append(choices, interaction.Choice{Label: l.Text("ux_d_wait"), Value: "wait"})
            }
            if !wire.Terminal(v.Status) {
                choices = append(choices, interaction.Choice{Label: l.Text("ux_d_stop"), Value: "stop"})
            }
        }
        terminal := wire.Terminal(v.Status) || s.saved.LastObservation != nil && wire.Terminal(s.saved.LastObservation.Status)
        if s.saved.Submission == "submitted" && (terminal || s.saved.CleanupPending) && c.uxRouteAvailable("clean") {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_d_clean"), Value: "clean"})
        }
    }
    // A subscription freezes this saved identity and does not query or start
    // it. Retained preparation/cleanup records are valid local sources too.
    if c.uxRouteAvailable("notify-run") {
        choices = append(choices, interaction.Choice{Label: l.Text("ux_d_subscribe"), Value: "notify-run"})
    }
    return append(choices, interaction.Choice{Label: l.Text("ux_core_back"), Value: "back"})
}

func (c *invocation) uxDStop(ctx context.Context, id string) (*execution.View, error) {
    review, err := c.app.Projects.PrepareStop(ctx, client.StopInput{ID: id})
    if err != nil {
        return nil, err
    }
    l := c.output.locale
    doc := interaction.Document{Rows: []interaction.Row{
        {Label: l.Text("ux_d_id"), Value: review.Run.ID},
        {Label: l.Text("ux_d_filter_target"), Value: review.Run.Target},
        {Label: l.Text("ux_d_grace"), Value: review.Grace.String()},
    }}
    if review.Run.Instance != nil {
        doc.Rows = append(doc.Rows, interaction.Row{Label: l.Text("ux_d_instance"), Value: review.Run.Instance.ID})
    }
    confirmed, err := c.input.Confirm(ctx, interaction.Confirmation{
        Title: l.Text("ux_d_stop_confirm"), Summary: &doc,
        Description: doc.Content(max(20, min(96, interaction.Width(c.options.Err))-8), false, c.input.Dark()),
        Kind:        interaction.ConfirmDefaultNo, AcceptLabel: l.Text("ux_d_stop"), RejectLabel: l.Text("ux_d_cancel"),
    })
    if err != nil || !confirmed {
        return nil, err
    }
    // Freeze the reviewed grace as an explicit override. Settings changing
    // while the confirmation is open must not change what the user approved.
    grace := review.Grace.String()
    var v execution.View
    callErr := c.input.WithNative(ctx, nil, func() error {
        var e error
        v, e = c.app.Projects.Stop(ctx, client.StopInput{ID: review.Run.ID, Grace: &grace, Policy: c.policy}, c.progress)
        return e
    })
    if ctx.Err() != nil {
        printRun(c.output.err, v, l)
        return &v, callErr
    }
    return &v, callErr
}

func (c *invocation) uxDStopResult(v execution.View, cause error) interaction.Document {
    l := c.output.locale
    title := l.Text("ux_d_stop_terminal")
    if cause != nil && !(wire.Terminal(v.Status) && v.QueryError == "") {
        title = l.Text("ux_d_stop_failed")
        var f *fault.Error
        if errors.As(cause, &f) && f.Code == "STOP_UNCONFIRMED" {
            title = l.Text("ux_d_stop_unknown")
        }
    }
    var body strings.Builder
    printRun(&body, v, l)
    if v.StopGraceMillis != nil {
        fmt.Fprintln(&body, l.Text("ux_d_stop_accepted", map[string]any{"Value": (time.Duration(*v.StopGraceMillis) * time.Millisecond).String()}))
    }
    if cause != nil {
        e := c.output.error(cause)
        fmt.Fprintln(&body, e.Code+": "+e.Message)
        if e.Hint != "" {
            fmt.Fprintln(&body, e.Hint)
        }
    }
    return interaction.Document{Title: title, Body: body.String()}
}

func (c *invocation) uxDWait(ctx context.Context, ids []string) (execution.WaitResult, error) {
    selected := slices.Clone(ids)
    l := c.output.locale
    duration := interaction.Field{Title: l.Text("ux_d_timeout"), Value: "30s", Validate: func(text string) error {
        d, err := time.ParseDuration(text)
        if err != nil || d <= 0 || d > 24*time.Hour {
            return errors.New(l.Text("wait_timeout"))
        }
        return nil
    }}
    if err := c.input.PageWithSummary(ctx, l.Text("ux_d_wait"), l.Text("ux_d_wait_help", map[string]any{"Count": len(selected)}), &duration); err != nil {
        return execution.WaitResult{}, err
    }
    timeout, _ := time.ParseDuration(duration.Value)
    var result execution.WaitResult
    cause := c.input.WithNative(ctx, nil, func() error {
        var err error
        result, err = c.app.Projects.Wait(ctx, selected, nil, timeout, c.policy, func(v execution.View) {
            fmt.Fprintln(c.output.err, c.uxDRunRow(v))
        })
        return err
    })
    doc := c.uxDWaitResult(result, cause)
    if ctx.Err() != nil {
        fmt.Fprintln(c.output.err, doc.Body)
        return result, cause
    }
    return result, c.uxShowDocument(ctx, doc)
}

func (c *invocation) uxDWaitResult(result execution.WaitResult, cause error) interaction.Document {
    l := c.output.locale
    title := l.Text("ux_d_wait_result")
    if result.TimedOut {
        title = l.Text("ux_d_wait_expired")
    }
    var body strings.Builder
    fmt.Fprintln(&body, l.Text("ux_d_wait_counts", map[string]any{
        "Selected": result.Selected, "Completed": result.Completed, "Pending": result.Pending,
        "Query": result.QueryErrors, "State": result.StateErrors, "Examined": result.Examined,
        "Unexamined": result.Unexamined, "Interrupted": result.Interrupted,
    }))
    for _, v := range result.Runs {
        printRun(&body, v, l)
    }
    if cause != nil {
        e := c.output.error(cause)
        fmt.Fprintln(&body, e.Code+": "+e.Message)
    }
    return interaction.Document{Title: title, Body: body.String()}
}
