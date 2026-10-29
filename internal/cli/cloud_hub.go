package cli

import (
    "context"
    "errors"
    "sort"
    "strings"
    "time"

    "charm.land/lipgloss/v2"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
)

// cloudHub owns navigation only. All operations use the same application
// requests as direct commands, with one active form/native stream at a time.
type cloudHub struct {
    call      *invocation
    selection client.CloudSelect
}

func (c *invocation) cloudHub(ctx context.Context) (any, error) {
    h := &cloudHub{call: c, selection: client.CloudSelect{Account: c.grammar.Cloud.Account, Product: c.grammar.Cloud.Product}}
    defer func() { c.output.nextActions = nil; c.output.cloudContext = nil; c.output.cloudWizard = false }()
    if h.selection.Account == nil {
        account := c.app.Resources.Settings.Cloud.DefaultAccount
        if account == "" && len(c.app.Resources.Settings.Cloud.Accounts) == 1 {
            for name := range c.app.Resources.Settings.Cloud.Accounts {
                account = name
            }
        }
        if account != "" {
            h.selection.Account = &account
        }
    }
    if h.selection.Product == nil && h.selection.Account != nil {
        product := c.app.Resources.Settings.Cloud.Accounts[*h.selection.Account].DefaultProduct
        if product != "" {
            h.selection.Product = &product
        }
    }
    for {
        summary := h.scope()
        if h.selection.Account != nil {
            var page cloud.Page
            err := c.cloudBusy(ctx, c.output.locale.Text("cloud_hub_loading"), summary, func(work context.Context) error {
                var e error
                page, e = c.app.Resources.CloudList(work, h.selection, 100)
                return e
            })
            summary += "\n" + c.output.locale.Text("cloud_hub_count", map[string]any{"Count": len(page.Instances)})
            if !page.Complete && err == nil {
                summary += "\n" + cloudCoverage(c.output.locale, page.Examined, page.Complete)
            }
            if err != nil {
                summary += "\n" + c.output.error(err).Message
            }
        } else {
            summary += "\n" + c.output.locale.Text("cloud_hub_no_account")
        }
        action, err := h.menu(ctx, "cloud_hub_title", summary, "instances", "instances", "market", "requests", "settings", "exit")
        if err != nil {
            if errors.Is(err, interaction.ErrCanceled) {
                return nil, nil
            }
            return nil, err
        }
        switch action {
        case "exit":
            return nil, nil
        case "scope":
            err = h.chooseScope(ctx)
        case "instances":
            err = h.instances(ctx)
        case "market":
            err = h.market(ctx)
        case "requests":
            err = h.requests(ctx)
        case "settings":
            err = h.settings(ctx)
        }
        if err != nil && !errors.Is(err, interaction.ErrCanceled) {
            if e := h.show(ctx, nil, err); e != nil {
                return nil, e
            }
        }
        settings, e := c.app.Resources.Config.Load()
        if e != nil {
            return nil, e
        }
        c.app.Resources.Settings, c.app.Machines.Settings, c.app.Projects.Settings = settings, settings, settings
    }
}

func (h *cloudHub) scope() string {
    account, product := "", ""
    if h.selection.Account != nil {
        account = *h.selection.Account
        product = h.call.app.Resources.Settings.Cloud.Accounts[account].DefaultProduct
    }
    if h.selection.Product != nil {
        product = *h.selection.Product
    }
    return cloudScope(h.call.output.locale, account, product, "")
}

func (h *cloudHub) menu(ctx context.Context, title, summary, value string, actions ...string) (string, error) {
    choices := []interaction.Choice{}
    for _, action := range actions {
        choices = append(choices, interaction.Choice{Label: h.call.output.locale.Text("cloud_hub_" + action), Value: action})
    }
    return h.call.input.SelectWithSummary(ctx, h.call.output.locale.Text(title), summary, choices, value)
}

func (h *cloudHub) chooseScope(ctx context.Context) error {
    names := []string{}
    for name := range h.call.app.Resources.Settings.Cloud.Accounts {
        names = append(names, name)
    }
    sort.Strings(names)
    choices := []interaction.Choice{}
    for _, name := range names {
        choices = append(choices, interaction.Choice{Label: name, Value: name})
    }
    if len(choices) == 0 {
        return h.login(ctx)
    }
    current := ""
    if h.selection.Account != nil {
        current = *h.selection.Account
    }
    name, err := h.call.input.SelectWithSummary(ctx, h.call.output.locale.Text("cloud_wizard_account"), h.scope(), choices, current)
    if err != nil {
        return err
    }
    product, err := h.call.input.Select(ctx, h.call.output.locale.Text("cloud_hub_product"), []interaction.Choice{{Label: h.call.output.locale.Text("cloud_ui_product_standard"), Value: "standard"}, {Label: h.call.output.locale.Text("cloud_ui_product_pro"), Value: "pro"}}, h.call.app.Resources.Settings.Cloud.Accounts[name].DefaultProduct)
    if err == nil {
        h.selection = client.CloudSelect{Account: &name, Product: &product}
    }
    return err
}

// action resolves each command's defaults through Kong, then invokes its typed
// handler with the shared input session. No user input is turned into argv.
func (h *cloudHub) action(ctx context.Context, command string, edit func(*grammar), profileOnly ...bool) (any, error) {
    model := &grammar{}
    parser, err := newParser(model)
    if err != nil {
        return nil, err
    }
    if _, err = parser.Parse(append([]string{"cloud"}, strings.Fields(command)...)); err != nil {
        return nil, err
    }
    model.Cloud.Account, model.Cloud.Product = h.selection.Account, h.selection.Product
    if edit != nil {
        edit(model)
    }
    child := *h.call
    child.profileOnly = len(profileOnly) > 0 && profileOnly[0]
    child.grammar = model
    old := child.output.command
    child.output.command = "cloud " + command
    defer func() { child.output.command = old; child.output.nextActions = nil; child.output.cloudWizard = false }()
    return child.cloud(ctx, "cloud "+command)
}

func (h *cloudHub) show(ctx context.Context, data any, cause error) error {
    c, l := h.call, h.call.output.locale
    body := ""
    if data != nil {
        var b strings.Builder
        printCloud(&b, data, l)
        body = strings.TrimSpace(b.String())
    }
    if cause != nil {
        e := c.output.error(cause)
        body += "\n" + e.Message
        if e.Hint != "" {
            body += "\n" + e.Hint
        }
    }
    lines := strings.Split(lipgloss.NewStyle().Width(max(20, min(96, interaction.Width(c.options.Err))-8)).Render(strings.TrimSpace(body)), "\n")
    size := max(3, interaction.Height(c.options.Err)-13)
    page := 0
    for {
        end := min(len(lines), (page+1)*size)
        summary := strings.Join(lines[page*size:end], "\n")
        choices := []interaction.Choice{}
        if len(lines) > size {
            summary += "\n" + l.Text("cloud_page_count", map[string]any{"Count": page + 1, "Value": (len(lines) + size - 1) / size})
        }
        if end < len(lines) {
            choices = append(choices, interaction.Choice{Label: l.Text("cloud_page_next"), Value: "next"})
        }
        if page > 0 {
            choices = append(choices, interaction.Choice{Label: l.Text("cloud_page_previous"), Value: "previous"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("cloud_hub_back"), Value: "back"})
        action, err := c.input.SelectWithSummary(ctx, l.Text("cloud_hub_result"), summary, choices, "back")
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

func (h *cloudHub) instances(ctx context.Context) error {
    c, l := h.call, h.call.output.locale
    filter := ""
    for {
        page, err := c.app.Resources.CloudList(ctx, h.selection, 100)
        choices := []interaction.Choice{}
        rows := append([]cloud.Instance{}, page.Instances...)
        sort.SliceStable(rows, func(i, j int) bool { return instanceLabel(rows[i], l) < instanceLabel(rows[j], l) })
        for _, row := range rows {
            if filter == "" || row.Status == filter {
                choices = append(choices, interaction.Choice{Label: instanceLabel(row, l), Value: row.Ref.ID})
            }
        }
        for _, action := range []string{"refresh", "filter", "back"} {
            choices = append(choices, interaction.Choice{Label: l.Text("cloud_hub_" + action), Value: "__" + action})
        }
        summary := h.scope() + "\n" + cloudCoverage(l, page.Examined, page.Complete)
        if err != nil {
            summary += "\n" + c.output.error(err).Message
        }
        id, e := c.input.SelectWithSummary(ctx, l.Text("cloud_hub_instances"), summary, choices, "")
        if e != nil {
            return e
        }
        switch id {
        case "__back":
            return nil
        case "__refresh":
            continue
        case "__filter":
            choices := []interaction.Choice{{Label: l.Text("cloud_hub_all"), Value: ""}}
            for _, status := range []string{"running", "stopped", "starting", "stopping", "unknown"} {
                choices = append(choices, interaction.Choice{Label: l.Text("cloud_status_" + status), Value: status})
            }
            filter, e = c.input.Select(ctx, l.Text("cloud_hub_filter"), choices, filter)
            if e != nil {
                return e
            }
            continue
        }
        var selected cloud.Instance
        for _, row := range rows {
            if row.Ref.ID == id {
                selected = row
                break
            }
        }
        if e = h.instance(ctx, selected); e != nil {
            return e
        }
    }
}

func instanceLabel(row cloud.Instance, l interface {
    Text(string, ...map[string]any) string
}) string {
    name := row.Name
    if len(row.Aliases) > 0 {
        name = strings.Join(row.Aliases, ", ")
    }
    if name == "" {
        name = "—"
    }
    return candidateCell(name, 14) + "  " + candidateCell(row.Ref.ID, 22) + "  " + candidateCell(l.Text("cloud_status_"+row.Status), 10) + "  " + row.Region
}

func (h *cloudHub) instance(ctx context.Context, row cloud.Instance) error {
    c := h.call
    for {
        settings, e := c.app.Resources.Config.Load()
        if e != nil {
            return e
        }
        c.app.Resources.Settings, c.app.Machines.Settings, c.app.Projects.Settings = settings, settings, settings
        row.Aliases = nil
        for name, target := range settings.Targets {
            if target.Cloud != nil && target.Cloud.Ref == row.Ref {
                row.Aliases = append(row.Aliases, name)
            }
        }
        sort.Strings(row.Aliases)
        fresh, err := c.app.Resources.CloudInfo(ctx, client.CloudInstanceInput{Select: h.selection, ID: row.Ref.ID})
        if err != nil {
            return err
        }
        fresh.Aliases = row.Aliases
        actions := []string{"info", "bind", "connection", "ssh", "use"}
        if fresh.Status == "running" {
            actions = append(actions, "stop")
        }
        if fresh.Status == "stopped" {
            actions = append(actions, "start", "clone")
        }
        actions = append(actions, "refresh", "back")
        action, e := h.menu(ctx, "cloud_hub_instance", h.scope()+"\n"+instanceLabel(fresh, c.output.locale), "info", actions...)
        if e != nil {
            return e
        }
        if action == "back" {
            return nil
        }
        if action == "refresh" {
            continue
        }
        alias := ""
        if len(row.Aliases) > 0 {
            alias = row.Aliases[0]
        }
        var result any
        switch action {
        case "info":
            result = fresh
        case "connection":
            result, e = c.app.Resources.CloudCredentials(ctx, client.CloudInstanceInput{Select: h.selection, ID: row.Ref.ID})
        case "start", "stop":
            result, e = h.action(ctx, action, func(g *grammar) {
                v := &g.Cloud.Start
                if action == "stop" {
                    v = &g.Cloud.Stop
                }
                v.ID = row.Ref.ID
                v.Wait = 2 * time.Minute
            })
        case "clone":
            result, e = h.action(ctx, "acquire", func(g *grammar) { g.Cloud.Acquire.CloneFrom = &row.Ref.ID })
        case "bind":
            result, e = h.action(ctx, "bind", func(g *grammar) { g.Cloud.Bind.Instance = row.Ref.ID; g.Cloud.Bind.Name = alias })
        case "ssh", "use":
            if alias == "" {
                result, e = h.action(ctx, "bind", func(g *grammar) { g.Cloud.Bind.Instance = row.Ref.ID })
                break
            }
            if action == "use" {
                result, e = c.app.Machines.UseTarget(ctx, alias)
            } else {
                e = c.app.Machines.SSH(ctx, alias, client.Streams{In: c.options.In, Out: c.options.Out, Err: c.options.Err})
            }
        }
        if e = h.show(ctx, result, e); e != nil {
            return e
        }
    }
}
