package cli

import (
    "context"
    "fmt"
    "sort"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
)

func (h *cloudHub) market(ctx context.Context) error {
    for {
        action, err := h.menu(ctx, "cloud_hub_market", h.scope(), "acquire", "acquire", "catalog_gpu", "catalog_region", "catalog_image", "search", "watch", "back")
        if err != nil {
            return err
        }
        if action == "back" {
            return nil
        }
        var result any
        switch action {
        case "acquire":
            result, err = h.action(ctx, "acquire", nil)
        case "catalog_gpu", "catalog_region", "catalog_image":
            category := strings.TrimPrefix(action, "catalog_")
            page, e := h.call.app.Resources.CloudCatalog(ctx, h.selection, cloud.CatalogInput{Category: category, Limit: 1000})
            if e != nil {
                err = e
                break
            }
            choices := []interaction.Choice{}
            if category == "region" {
                for _, group := range regionGroups(page.Items) {
                    choices = append(choices, interaction.Choice{Label: group.label, Value: group.ids[0]})
                }
            } else {
                for _, item := range page.Items {
                    choices = append(choices, interaction.Choice{Label: item.Name, Value: item.ID})
                }
            }
            choices = append(choices, interaction.Choice{Label: h.call.output.locale.Text("cloud_hub_back"), Value: "__back"})
            _, err = h.call.input.SelectWithSummary(ctx, h.call.output.locale.Text("cloud_hub_"+action), h.scope()+"\n"+cloudCoverage(h.call.output.locale, page.Examined, page.Complete), choices, "")
            continue
        case "search", "watch":
            input, e := h.marketInput(ctx, action == "watch")
            if e != nil {
                return e
            }
            if action == "search" {
                if err = h.search(ctx, input); err != nil {
                    return err
                }
                continue
            } else {
                work, cancel := context.WithCancel(ctx)
                feedback := &cloudFeedback{call: h.call, mode: "auto", ctx: work, cancel: cancel}
                if h.selection.Account != nil {
                    feedback.account = *h.selection.Account
                }
                result, err = h.call.app.Resources.CloudWatch(work, input, feedback.emit)
                closeErr := feedback.close()
                cancel()
                if closeErr != nil {
                    return closeErr
                }
            }
        }
        if e := h.show(ctx, result, err); e != nil {
            return e
        }
    }
}

func (h *cloudHub) marketInput(ctx context.Context, watch bool) (client.CloudSearchInput, error) {
    c, l := h.call, h.call.output.locale
    input := client.CloudSearchInput{Select: h.selection, Limit: 100}
    plan, err := c.app.Resources.CloudSearchPlan(ctx, input)
    if err != nil {
        return input, err
    }
    d := cloudDraft{browse: true, mode: "auto", plan: plan, in: client.CloudAcquireInput{Select: h.selection, Plan: input.Plan}, step: 0}
    if back, e := d.compute(ctx, c); e != nil {
        return input, e
    } else if back {
        return input, interaction.ErrCanceled
    }
    price := &interaction.Field{Title: l.Text("cloud_optional_price"), Validate: func(s string) error {
        if s == "" {
            return nil
        }
        _, e := cloud.ParseMoney(s, "CNY")
        if e != nil {
            return fmt.Errorf("%s", l.Text("cloud_budget_required"))
        }
        return nil
    }}
    if plan.Requirements.MaxComputeHourlyPrice != nil {
        price.Value = plan.Requirements.MaxComputeHourlyPrice.Amount
    }
    fields := []*interaction.Field{price}
    duration := &interaction.Field{Title: l.Text("cloud_wizard_duration"), Value: "10m", Validate: func(s string) error {
        d, e := time.ParseDuration(s)
        if e != nil || d <= 0 || d > 24*time.Hour {
            return fmt.Errorf("%s", l.Text("cloud_wait_invalid"))
        }
        return nil
    }}
    if watch {
        fields = append(fields, duration)
    }
    if err := c.input.PageWithSummary(ctx, l.Text("cloud_hub_search"), h.scope(), fields...); err != nil {
        return input, err
    }
    d.in.Plan.Explicit.MaxComputeHourlyPrice = &price.Value
    if watch {
        d.in.Plan.Explicit.WaitTimeout = &duration.Value
    }
    input.Plan = d.in.Plan
    return input, nil
}

func (h *cloudHub) search(ctx context.Context, input client.CloudSearchInput) error {
    l := h.call.output.locale
    order, selected := "price", ""
    var page cloud.SearchResult
    reload := true
    for {
        if reload {
            var err error
            page, err = h.call.app.Resources.CloudSearch(ctx, input)
            if err != nil {
                return h.show(ctx, page, err)
            }
            reload = false
        }
        rows := append([]cloud.Offer{}, page.Matches...)
        sortCandidates(rows, order)
        summary := h.scope() + "\n" + cloudCoverage(l, page.Examined, page.Complete)
        fields := []*interaction.Field{}
        if len(rows) > 0 {
            candidate := &interaction.Field{Title: l.Text("cloud_wizard_host"), Value: selected}
            for _, row := range rows {
                candidate.Choices = append(candidate.Choices, interaction.Choice{Label: candidateRow(row, h.call.output.regionCatalog, l.Text("cloud_price_unknown")), Value: row.ID})
            }
            candidate.Detail = func(id string) string {
                for _, row := range rows {
                    if row.ID == id {
                        return l.Text("cloud_candidates_columns") + "\n" + row.ID + " · " + row.Region + "\n" + offerDetail(row, l)
                    }
                }
                return ""
            }
            fields = append(fields, candidate)
        } else {
            summary += "\n" + l.Text("cloud_no_match")
        }
        actions := []string{"refresh", "sort"}
        if page.HasMore {
            actions = append(actions, "more")
        }
        actions = append(actions, "back")
        nav := &interaction.Field{Title: l.Text("cloud_browse_actions"), Value: "back"}
        for _, action := range actions {
            label := l.Text("cloud_hub_back")
            if action != "back" {
                label = l.Text("cloud_candidates_" + action)
            }
            nav.Choices = append(nav.Choices, interaction.Choice{Label: label, Value: action})
        }
        fields = append(fields, nav)
        if err := h.call.input.PageWithSummary(ctx, l.Text("cloud_hub_search"), summary, fields...); err != nil {
            return err
        }
        if len(rows) > 0 {
            selected = fields[0].Value
        }
        switch nav.Value {
        case "back":
            return nil
        case "refresh":
            reload = true
        case "more":
            input.Offset = page.NextOffset
            reload = true
        case "sort":
            choices := []interaction.Choice{}
            for _, value := range []string{"price", "cpu", "memory"} {
                choices = append(choices, interaction.Choice{Label: l.Text("cloud_candidates_sort_" + value), Value: value})
            }
            var err error
            order, err = h.call.input.Select(ctx, l.Text("cloud_candidates_sort"), choices, order)
            if err != nil {
                return err
            }
        }
    }
}

func (h *cloudHub) requests(ctx context.Context) error {
    for {
        result, err := h.call.app.Resources.CloudRequests(ctx, client.CloudSelect{})
        choices := []interaction.Choice{}
        rows := append([]cloud.Receipt{}, result.Requests...)
        pending := func(r cloud.Receipt) bool {
            switch r.Status {
            case "intent", "uncertain", "created", "accepted":
                return true
            }
            return false
        }
        sort.SliceStable(rows, func(i, j int) bool {
            if pending(rows[i]) != pending(rows[j]) {
                return pending(rows[i])
            }
            return rows[i].CreatedAt.After(rows[j].CreatedAt)
        })
        for _, row := range rows {
            label := fmt.Sprintf("%s · %s/%s · %s · %s", row.Target, row.Account, row.Plan.Product, h.call.output.locale.Text("cloud_status_"+row.Status), row.ID)
            choices = append(choices, interaction.Choice{Label: label, Value: row.ID})
        }
        choices = append(choices, interaction.Choice{Label: h.call.output.locale.Text("cloud_hub_back"), Value: "__back"})
        summary := ""
        if err != nil {
            summary = h.call.output.error(err).Message
        }
        id, e := h.call.input.SelectWithSummary(ctx, h.call.output.locale.Text("cloud_hub_requests"), summary, choices, "")
        if e != nil {
            return e
        }
        if id == "__back" {
            return nil
        }
        receipt, e := h.call.app.Resources.CloudRequest(ctx, client.CloudSelect{}, id)
        if e != nil {
            return e
        }
        actions := []string{"request_info"}
        if receipt.Operation == "acquire" && receipt.Instance != nil && receipt.Status != "uncertain" && receipt.Status != "intent" {
            actions = append(actions, "resume")
        }
        if receipt.Operation == "acquire" && (receipt.Status == "uncertain" || receipt.Status == "intent") {
            actions = append(actions, "associate", "absent")
        }
        if receipt.Operation != "acquire" {
            actions = append(actions, "observe")
        }
        actions = append(actions, "back")
        action, e := h.menu(ctx, "cloud_hub_request", fmt.Sprintf("%s · %s · %s", receipt.ID, receipt.Target, receipt.Status), "request_info", actions...)
        if e != nil {
            return e
        }
        var data any
        switch action {
        case "back":
            continue
        case "request_info":
            data = receipt
        case "resume":
            data, e = h.action(ctx, "bind", func(g *grammar) { g.Cloud.Account = nil; g.Cloud.Product = nil; g.Cloud.Bind.Request = receipt.ID })
        case "observe":
            data, e = h.call.app.Resources.ObserveCloudRequest(ctx, receipt.ID)
        case "associate":
            value, err := h.call.input.Text(ctx, interaction.TextPrompt{Title: h.call.output.locale.Text("cloud_hub_associate_id")})
            if err != nil {
                return err
            }
            data, e = h.call.app.Resources.CloudReconcile(ctx, client.CloudReconcileInput{Request: receipt.ID, Instance: value}, h.call.approveCloud)
        case "absent":
            data, e = h.call.app.Resources.CloudReconcile(ctx, client.CloudReconcileInput{Request: receipt.ID, NoInstance: true}, h.call.approveCloud)
        }
        if e = h.show(ctx, data, e); e != nil {
            return e
        }
    }
}

func (h *cloudHub) settings(ctx context.Context) error {
    for {
        action, err := h.menu(ctx, "cloud_hub_settings", h.scope(), "scope", "scope", "login", "default", "profiles", "logout", "remove_account", "back")
        if err != nil {
            return err
        }
        var data any
        switch action {
        case "back":
            return nil
        case "scope":
            err = h.chooseScope(ctx)
        case "login":
            err = h.login(ctx)
        case "profiles":
            err = h.profiles(ctx)
        case "default":
            if h.selection.Account != nil {
                data, err = h.call.app.Resources.UseCloudAccount(ctx, *h.selection.Account, h.selection.Product)
            }
        case "logout", "remove_account":
            if h.selection.Account == nil {
                continue
            }
            yes, e := h.call.input.Confirm(ctx, interaction.Confirmation{Title: h.call.output.locale.Text("cloud_hub_confirm_"+action, map[string]any{"Account": *h.selection.Account}), Kind: interaction.ConfirmDefaultNo, AcceptLabel: h.call.output.locale.Text("yes"), RejectLabel: h.call.output.locale.Text("no")})
            if e != nil {
                return e
            }
            if !yes {
                continue
            }
            if action == "logout" {
                data, err = h.call.app.Resources.Logout(ctx, *h.selection.Account)
            } else {
                data, err = h.call.app.Resources.RemoveCloudAccount(ctx, *h.selection.Account)
                h.selection = client.CloudSelect{}
            }
        }
        settings, e := h.call.app.Resources.Config.Load()
        if e != nil {
            return e
        }
        h.call.app.Resources.Settings, h.call.app.Machines.Settings, h.call.app.Projects.Settings = settings, settings, settings
        if err != nil {
            if e := h.show(ctx, nil, err); e != nil {
                return e
            }
        } else if data != nil {
            if e := h.show(ctx, data, nil); e != nil {
                return e
            }
        }
    }
}

func (h *cloudHub) login(ctx context.Context) error {
    name := ""
    if h.selection.Account != nil {
        name = *h.selection.Account
    }
    if name == "" {
        var err error
        name, err = h.call.input.Text(ctx, interaction.TextPrompt{Title: h.call.output.locale.Text("cloud_wizard_account_name"), Validate: func(s string) error {
            if !config.ValidName(s) {
                return fmt.Errorf("%s", h.call.output.locale.Text("cloud_name_required"))
            }
            return nil
        }})
        if err != nil {
            return err
        }
    }
    child := *h.call
    model := &grammar{}
    model.Cloud.Login.Name = name
    model.Cloud.Login.RememberPassword = true
    child.grammar = model
    _, err := child.login(ctx)
    if err == nil {
        settings, e := h.call.app.Resources.Config.Load()
        if e != nil {
            return e
        }
        h.call.app.Resources.Settings = settings
        h.selection.Account = &name
        if h.selection.Product == nil {
            product := settings.Cloud.Accounts[name].DefaultProduct
            h.selection.Product = &product
        }
    }
    return err
}

func (h *cloudHub) profiles(ctx context.Context) error {
    for {
        rows, err := h.call.app.Resources.CloudProfiles(ctx, "")
        if err != nil {
            return err
        }
        choices := []interaction.Choice{}
        for _, row := range rows {
            source := h.call.output.locale.Text("cloud_profile_source_user")
            if row.Project {
                source = h.call.output.locale.Text("cloud_profile_source_project")
                if row.User {
                    source += " · " + h.call.output.locale.Text("cloud_profile_user_overridden")
                }
            }
            choices = append(choices, interaction.Choice{Label: row.Name + " · " + source, Value: row.Name})
        }
        choices = append(choices, interaction.Choice{Label: h.call.output.locale.Text("cloud_profile_create"), Value: "__create"}, interaction.Choice{Label: h.call.output.locale.Text("cloud_hub_back"), Value: "__back"})
        name, err := h.call.input.SelectWithSummary(ctx, h.call.output.locale.Text("cloud_hub_profiles"), h.scope(), choices, "")
        if err != nil {
            return err
        }
        if name == "__back" {
            return nil
        }
        if name == "__create" {
            data, e := h.action(ctx, "acquire", nil, true)
            if e = h.show(ctx, data, e); e != nil {
                return e
            }
            continue
        }
        profiles, err := h.call.app.Resources.CloudProfiles(ctx, name)
        if err != nil {
            return err
        }
        actions := []string{"profile_info", "profile_rent", "profile_edit"}
        if len(profiles) > 0 && profiles[0].User {
            actions = append(actions, "profile_remove")
        }
        actions = append(actions, "back")
        action, err := h.menu(ctx, "cloud_hub_profiles", name, "profile_info", actions...)
        if err != nil {
            return err
        }
        var data any
        switch action {
        case "back":
            continue
        case "profile_info":
            data = cloudProfilesResult{Profiles: profiles}
        case "profile_rent":
            data, err = h.action(ctx, "acquire", func(g *grammar) { g.Cloud.Acquire.Profile = name })
        case "profile_edit":
            data, err = h.action(ctx, "acquire", func(g *grammar) { g.Cloud.Acquire.Profile = name }, true)
        case "profile_remove":
            accepted, e := h.call.input.Confirm(ctx, interaction.Confirmation{Title: h.call.output.locale.Text("cloud_profile_remove_confirm", map[string]any{"Target": name}), Kind: interaction.ConfirmDefaultNo, AcceptLabel: h.call.output.locale.Text("yes"), RejectLabel: h.call.output.locale.Text("no")})
            if e != nil {
                return e
            }
            if !accepted {
                continue
            }
            remaining, e := h.call.app.Resources.RemoveCloudProfile(ctx, name)
            data, err = cloudProfilesResult{Profiles: remaining}, e
        }
        if e := h.show(ctx, data, err); e != nil {
            return e
        }
    }
}
