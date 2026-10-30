package cli

import (
    "context"
    "errors"
    "net/url"
    "regexp"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

func (c *invocation) uxIDestinationDocument(d client.DestinationRecord) interaction.Document {
    doc := interaction.Document{Title: c.output.locale.Text("ux_i_destination"), Rows: []interaction.Row{
        c.uxIRow("name", d.Name), c.uxIRow("adapter", d.Kind), c.uxIRow("session", d.SessionID),
    }, Footer: c.output.locale.Text("ux_i_binding_scope")}
    if !d.CreatedAt.IsZero() {
        doc.Rows = append(doc.Rows, c.uxIRow("created", uxITime(d.CreatedAt)))
    }
    if d.Experimental {
        doc.Body = c.output.locale.Text("ux_i_experimental")
    }
    return doc
}

func (c *invocation) uxIDestinations(ctx context.Context) error {
    query, offset := "", 0
    for {
        rows, err := c.app.Notify.Destinations(ctx)
        if err != nil {
            return err
        }
        filtered := []client.DestinationRecord{}
        for _, d := range rows {
            if uxIMatches(query, d.Name, d.Kind, d.SessionID) {
                filtered = append(filtered, d)
            }
        }
        if offset >= len(filtered) {
            offset = 0
        }
        end := min(len(filtered), offset+c.uxIPageSize())
        choices := []interaction.Choice{}
        for _, d := range filtered[offset:end] {
            choices = append(choices, interaction.Choice{Label: c.uxILabel(d.Name + " · " + d.Kind + " · " + d.SessionID), Value: "item:" + d.Name})
        }
        if end < len(filtered) {
            choices = append(choices, c.uxIChoice("next"))
        }
        if offset > 0 {
            choices = append(choices, c.uxIChoice("previous"))
        }
        for _, action := range []string{"bind", "add", "search", "refresh", "back"} {
            choices = append(choices, c.uxIChoice(action))
        }
        summary := c.uxICoverage(end-offset, end < len(filtered))
        if len(rows) == 0 {
            summary = c.output.locale.Text("ux_i_no_destinations")
        }
        action, err := c.input.SelectWithSummary(ctx, c.output.locale.Text("ux_i_destinations"), summary, choices, "back")
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "next":
            offset = end
        case "previous":
            offset = max(0, offset-c.uxIPageSize())
        case "search":
            query, err = c.uxIText(ctx, "ux_i_search_prompt")
            offset = 0
        case "bind":
            err = c.uxIBindCurrent(ctx)
        case "add":
            err = c.uxIAddDestination(ctx)
        case "refresh":
        default:
            for _, d := range filtered {
                if action == "item:"+d.Name {
                    err = c.uxIDestination(ctx, d)
                    break
                }
            }
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return err
        }
    }
}

func (c *invocation) uxIDestination(ctx context.Context, d client.DestinationRecord) error {
    for {
        action, err := c.uxIMenu(ctx, "ux_i_destination", c.uxILabel(d.Name+" · "+d.Kind+" · "+d.SessionID), "details", "probe", "remove", "back")
        if err != nil || action == "back" {
            return err
        }
        switch action {
        case "details":
            err = c.uxShowDocument(ctx, c.uxIDestinationDocument(d))
        case "probe":
            setup, ok := c.app.Notify.(client.NotificationSetupService)
            if !ok {
                err = asyncUnavailable("notify destination")
                break
            }
            preview, cause := setup.ProbeDestination(ctx, d.Name)
            err = c.uxIShowCapabilities(ctx, preview, cause)
        case "remove":
            control, e := c.uxIControl()
            if e != nil {
                return e
            }
            if err = c.uxShowDocument(ctx, c.uxIDestinationDocument(d)); err != nil {
                return err
            }
            yes, e := c.uxIConfirm(ctx, "ux_i_remove_destination", c.output.locale.Text("ux_i_remove_destination_scope"))
            if e != nil || !yes {
                return e
            }
            e = control.RemoveDestination(ctx, d.Name)
            return c.uxShowResult(ctx, destinationRemoval{Name: d.Name, Removed: e == nil}, e)
        }
        if err = c.uxIHandle(ctx, err); err != nil {
            return err
        }
    }
}

func (c *invocation) uxIShowCapabilities(ctx context.Context, p client.NotificationDestinationPreview, cause error) error {
    doc := c.uxIDestinationDocument(p.Destination)
    caps := p.Capabilities
    yesNo := func(v bool) string {
        if v {
            return c.output.locale.Text("yes")
        }
        return c.output.locale.Text("no")
    }
    doc.Rows = append(doc.Rows, c.uxIRow("checked", uxITime(p.CheckedAt)), c.uxIRow("available", yesNo(caps.Available)),
        c.uxIRow("existing_session", yesNo(caps.ExistingSession)), c.uxIRow("receipt_query", yesNo(caps.ReceiptQueryable)),
        c.uxIRow("processing_ack", yesNo(caps.ProcessingAcknowledgement)), c.uxIRow("idempotent", yesNo(caps.Idempotent)), c.uxIRow("code", caps.ReasonCode))
    if p.Destination.Kind == "webhook" {
        doc.Body = c.output.locale.Text("ux_i_webhook_probe")
    }
    if cause != nil {
        view := c.output.error(cause)
        doc.Body += "\n" + view.Message + "\n" + view.Hint
    }
    return c.uxShowDocument(ctx, doc)
}

func (c *invocation) uxIName(ctx context.Context) (string, error) {
    return c.input.Text(ctx, interaction.TextPrompt{Title: c.output.locale.Text("ux_i_name"), MaxBytes: 128, Validate: func(v string) error {
        if !config.ValidName(v) {
            return errors.New(c.output.locale.Text("notify_destination_invalid"))
        }
        return nil
    }})
}

func (c *invocation) uxIPreviousDestination(ctx context.Context, name string) (bool, error) {
    rows, err := c.app.Notify.Destinations(ctx)
    if err != nil {
        return false, err
    }
    for _, d := range rows {
        if d.Name == name {
            doc := c.uxIDestinationDocument(d)
            doc.Title = c.output.locale.Text("ux_i_previous_binding")
            if err := c.uxShowDocument(ctx, doc); err != nil {
                return true, err
            }
            return true, nil
        }
    }
    return false, nil
}

func (c *invocation) uxIBindCurrent(ctx context.Context) error {
    setup, ok := c.app.Notify.(client.NotificationSetupService)
    binder, canBind := c.app.Notify.(client.CurrentBindingService)
    if !ok || !canBind {
        return asyncUnavailable("notify bind --current")
    }
    preview, err := setup.PreviewCurrentBinding(ctx)
    if err != nil {
        return c.uxIShowCapabilities(ctx, preview, err)
    }
    if err = c.uxIShowCapabilities(ctx, preview, nil); err != nil {
        return err
    }
    name, err := c.uxIName(ctx)
    if err != nil {
        return err
    }
    replace, err := c.uxIPreviousDestination(ctx, name)
    if err != nil {
        return err
    }
    prompt := "ux_i_bind_confirm"
    if replace {
        prompt = "ux_i_replace_confirm"
    }
    yes, err := c.uxIConfirm(ctx, prompt, c.uxILabel(name+" → "+preview.Destination.SessionID))
    if err != nil || !yes {
        return err
    }
    result, err := binder.BindCurrent(ctx, client.BindCurrentInput{Name: name, Replace: replace})
    return c.uxShowResult(ctx, result, err)
}

var uxIBearerName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func uxIEndpointValid(value string) bool {
    // URLs with credentials/query/fragment must not become review text or argv.
    // Socket endpoints are private absolute paths validated by the SDK.
    u, err := url.Parse(value)
    return err == nil && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.ContainsAny(value, "\x00\r\n")
}

func (c *invocation) uxIAddDestination(ctx context.Context) error {
    name, err := c.uxIName(ctx)
    if err != nil {
        return err
    }
    choices := []interaction.Choice{}
    for _, kind := range []string{"webhook", "codex", "codex-desktop", "claude-channel"} {
        choices = append(choices, interaction.Choice{Label: kind, Value: kind})
    }
    choices = append(choices, c.uxIChoice("back"))
    kind, err := c.input.Select(ctx, c.output.locale.Text("ux_i_adapter"), choices, "webhook")
    if err != nil || kind == "back" {
        return err
    }
    l := c.output.locale
    endpoint := &interaction.Field{Title: l.Text("ux_i_endpoint"), Description: l.Text("ux_i_endpoint_hint"), Validate: func(s string) error {
        if !uxIEndpointValid(s) {
            return errors.New(l.Text("notify_destination_invalid"))
        }
        return nil
    }}
    session := &interaction.Field{Title: l.Text("ux_i_session")}
    cwd := &interaction.Field{Title: l.Text("ux_i_cwd")}
    bearer := &interaction.Field{Title: l.Text("ux_i_bearer"), Description: l.Text("ux_i_bearer_hint"), Validate: func(s string) error {
        if s != "" && !uxIBearerName.MatchString(s) {
            return errors.New(l.Text("notify_destination_invalid"))
        }
        return nil
    }}
    for {
        fields := []*interaction.Field{endpoint}
        if kind != "webhook" {
            fields = append(fields, session)
        }
        if kind == "codex" || kind == "claude-channel" {
            fields = append(fields, cwd)
        }
        if kind == "webhook" {
            fields = append(fields, bearer)
        }
        if err = c.input.Page(ctx, l.Text("ux_i_add"), fields...); err != nil {
            return err
        }
        d := agentdelivery.Destination{Kind: kind, Endpoint: endpoint.Value, SessionID: session.Value, CWD: cwd.Value, BearerRef: bearer.Value}
        if kind == "webhook" && strings.HasPrefix(d.Endpoint, "http://") {
            yes, e := c.uxIConfirm(ctx, "ux_i_http_confirm", l.Text("ux_i_http_risk"))
            if e != nil || !yes {
                return e
            }
            d.AllowInsecureHTTP = true
        }
        if d.Validate() != nil {
            if err = c.uxIHandle(ctx, fault.Invalid("--kind/--session/--endpoint", "invalid_value", "notify_destination_invalid")); err != nil {
                return err
            }
            continue
        }
        doc := c.uxIDestinationDocument(client.DestinationRecord{Name: name, Kind: kind, SessionID: d.SessionID, Experimental: kind == "codex-desktop"})
        doc.Rows = append(doc.Rows, c.uxIRow("endpoint", d.Endpoint), c.uxIRow("cwd", d.CWD), c.uxIRow("bearer", d.BearerRef))
        doc.Body = l.Text("ux_i_save_binding_hint")
        if err = c.uxShowDocument(ctx, doc); err != nil {
            return err
        }
        replace, err := c.uxIPreviousDestination(ctx, name)
        if err != nil {
            return err
        }
        action, err := c.uxIMenu(ctx, "ux_i_review", "", "save", "edit", "back")
        if err != nil || action == "back" {
            return err
        }
        if action == "edit" {
            continue
        }
        if replace {
            yes, e := c.uxIConfirm(ctx, "ux_i_replace_confirm", name)
            if e != nil || !yes {
                return e
            }
        }
        result, err := c.app.Notify.PutDestination(ctx, client.DestinationInput{Name: name, Destination: d, Replace: replace})
        return c.uxShowResult(ctx, result, err)
    }
}
