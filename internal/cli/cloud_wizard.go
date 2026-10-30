package cli

import (
    "context"
    "errors"
    "fmt"
    "reflect"
    "sort"
    "strconv"
    "strings"

    "golang.org/x/text/width"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
)

type cloudDraft struct {
    environmentReset bool
    approved         *cloudApproved
    offset           int
    in               client.CloudAcquireInput
    mode             string
    stage            cloudStage
    selected         *cloud.Offer
    models, regions  []cloud.CatalogItem
    plan             cloud.Plan
    cudaExplicit     bool
}

func cloudPointer[T any](v T) *T { return &v }

func (d *cloudDraft) scope(c *invocation) string {
    return d.scopeAt(c, min(int(d.stage)+1, 4))
}

func (d *cloudDraft) scopeAt(c *invocation, current int) string {
    account := ""
    if d.in.Select.Account != nil {
        account = *d.in.Select.Account
    }
    title := cloudScope(c.output.locale, account, d.plan.Product, d.in.Name)
    if d.environmentReset {
        title += "\n" + c.output.locale.Text("cloud_scope_environment_reset")
    }
    if d.plan.Clone != nil {
        title += "\n" + c.output.locale.Text("cloud_clone_summary", map[string]any{"ID": d.plan.Clone.Instance.ID, "Region": d.plan.Clone.Region})
    }
    return withCloudRoadmap(title, c.output.locale, current)
}

func (d *cloudDraft) title(c *invocation, id string) string {
    return c.output.locale.Text(id) + "\n" + d.scope(c)
}

func (d *cloudDraft) selectChoice(ctx context.Context, c *invocation, id string, choices []interaction.Choice, value string) (string, error) {
    current := min(int(d.stage)+1, 4)
    switch id {
    case "cloud_wizard_mode":
        current = 0
    case "cloud_candidate_changed":
        current = 2
    }
    return c.input.SelectWithSummary(ctx, c.output.locale.Text(id), d.scopeAt(c, current), choices, value)
}

func (d *cloudDraft) page(ctx context.Context, c *invocation, id string, fields ...*interaction.Field) error {
    return c.input.PageWithSummary(ctx, c.output.locale.Text(id), d.scope(c), fields...)
}

func (d *cloudDraft) catalog(ctx context.Context, c *invocation, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    if d.in.CloneFrom != nil {
        return c.app.Resources.CloudCloneCatalog(ctx, d.in.Select, *d.in.CloneFrom, in)
    }
    return c.app.Resources.CloudCatalog(ctx, d.in.Select, in)
}

func (d *cloudDraft) search(ctx context.Context, c *invocation) (cloud.SearchResult, error) {
    in := client.CloudSearchInput{Select: d.in.Select, Plan: d.in.Plan, Limit: d.in.Limit, Offset: d.offset}
    if d.in.CloneFrom != nil {
        return c.app.Resources.CloudCloneSearch(ctx, in, *d.in.CloneFrom)
    }
    return c.app.Resources.CloudSearch(ctx, in)
}

func (d *cloudDraft) navigation(c *invocation, next string) []interaction.Choice {
    l := c.output.locale
    return []interaction.Choice{{Label: l.Text("cloud_nav_" + next), Value: "next"}, {Label: l.Text("cloud_nav_back"), Value: "back"}, {Label: l.Text("cloud_nav_cancel"), Value: "cancel"}}
}

func (d *cloudDraft) navField(c *invocation) *interaction.Field {
    return &interaction.Field{Title: c.output.locale.Text("cloud_wizard_navigation"), Value: "next", Choices: d.navigation(c, "next")}
}

func (d *cloudDraft) setup(ctx context.Context, c *invocation) error {
    d.move(cloudRestart)
    r, l := c.app.Resources, c.output.locale
    d.cudaExplicit = d.in.Plan.Explicit.AutoDL.CUDAFrom != nil
    accounts := r.Settings.Cloud.Accounts
    name := r.Settings.Cloud.DefaultAccount
    frozenSource := false
    if d.in.CloneFrom != nil {
        if t, ok := r.Settings.Targets[*d.in.CloneFrom]; ok && t.Cloud != nil {
            name = t.Cloud.Ref.Account
            frozenSource = true
        }
    }
    if d.in.Select.Account != nil {
        name = *d.in.Select.Account
        if _, ok := accounts[name]; !ok && !frozenSource {
            return fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
        }
    }
    if name == "" {
        names := make([]string, 0, len(accounts))
        for n := range accounts {
            names = append(names, n)
        }
        sort.Strings(names)
        if len(names) == 0 {
            name = "__login"
        } else if len(names) == 1 {
            name = names[0]
        } else {
            choices := []interaction.Choice{}
            for _, n := range names {
                choices = append(choices, interaction.Choice{Label: n, Value: n})
            }
            choices = append(choices, interaction.Choice{Label: l.Text("cloud_wizard_login"), Value: "__login"})
            var err error
            name, err = c.input.Select(ctx, l.Text("cloud_wizard_account"), choices, "")
            if err != nil {
                return err
            }
        }
    }
    if name == "__login" || name == "" {
        v, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("cloud_wizard_account_name"), Validate: func(v string) error {
            if !config.ValidName(v) {
                return errors.New(l.Text("cloud_name_required"))
            }
            return nil
        }})
        if err != nil {
            return err
        }
        if _, err := c.loginRequest(ctx, client.LoginInput{Name: v, Product: copyCloudValue(d.in.Select.Product), Remember: true}); err != nil {
            return err
        }
        accounts = r.Settings.Cloud.Accounts
        name = v
    }
    d.in.Select.Account = cloudPointer(name)
    d.mode = "select"
    if d.in.CloneFrom != nil || d.in.Plan.Explicit.WaitTimeout != nil || d.in.Plan.Explicit.Continuous != nil && *d.in.Plan.Explicit.Continuous {
        d.mode = "auto"
    }
    if d.in.Name == "" {
        d.in.Name = "gpu"
        for n := 1; ; n++ {
            if _, exists := r.Settings.Targets[d.in.Name]; !exists {
                break
            }
            d.in.Name = fmt.Sprintf("gpu-%d", n)
        }
    }
    preview, e := r.CloudPreview(ctx, d.in)
    if preview.Plan.Requirements.GPUCount > 0 {
        d.plan = preview.Plan
    } else if e != nil {
        return e
    }
    if d.in.CloneFrom != nil {
        account, plan, err := r.CloudClonePlan(ctx, d.in)
        if err != nil {
            return err
        }
        d.in.Select.Account, d.plan = cloudPointer(account), plan
        source := plan.Clone
        d.in.Plan.Explicit.GPUVendor = cloudPointer(source.Vendor)
        d.in.Plan.Explicit.Regions = append([]string{}, plan.Requirements.Regions...)
        d.in.Plan.Explicit.AutoDL.ExpandDataDiskGB = cloudPointer(plan.AutoDL.ExpandDataDiskGB)
        d.in.Plan.Compatibility = &cloud.ImageCompatibility{Vendor: source.Vendor, Architecture: source.Architecture, CUDA: source.CUDA}
    }
    if d.in.Select.Product == nil {
        d.in.Select.Product = cloudPointer(d.plan.Product)
    }
    if d.in.CloneFrom == nil {
        d.normalizeProductEnvironment(accounts[name].Credential.Mode)
    }
    if e == nil && d.plan.Requirements.MaxComputeHourlyPrice != nil {
        d.mode = "auto"
        if d.in.OfferID != "" {
            d.mode = "select"
        }
        d.move(cloudStartReview)
        return nil
    }
    mode, err := d.selectChoice(ctx, c, "cloud_wizard_mode", []interaction.Choice{{Label: l.Text("cloud_mode_select"), Value: "select"}, {Label: l.Text("cloud_mode_auto"), Value: "auto"}}, d.mode)
    if err != nil {
        return err
    }
    d.mode = mode
    err = c.cloudBusy(ctx, l.Text("cloud_loading_catalog"), d.scope(c), func(work context.Context) error {
        page, e := d.catalog(work, c, cloud.CatalogInput{Category: "gpu", Limit: 1000})
        if e != nil {
            return e
        }
        d.models = page.Items
        page, e = d.catalog(work, c, cloud.CatalogInput{Category: "region", Limit: 1000})
        if e != nil {
            return e
        }
        d.regions = page.Items
        c.output.regionCatalog = page.Items
        return nil
    })
    if err != nil {
        return err
    }
    if len(d.models) == 0 {
        return fault.New("NO_MATCH", "cloud_no_match", nil)
    }
    return nil
}

// Only the human draft resets incompatible inherited fields. Explicit command
// inputs and application-level preference resolution retain their contracts.
func (d *cloudDraft) normalizeProductEnvironment(mode string) {
    p, env := &d.in.Plan.Explicit.AutoDL, &d.plan.AutoDL
    clearInt := func(input **int, value *int) {
        if *input == nil && *value != 0 {
            *input = cloudPointer(0)
            *value = 0
            d.environmentReset = true
        }
    }
    clearString := func(input **string, value *string) {
        if *input == nil && *value != "" {
            *input = cloudPointer("")
            *value = ""
            d.environmentReset = true
        }
    }
    if d.plan.Product == "standard" {
        clearInt(&p.CUDAFrom, &env.CUDAFrom)
        clearInt(&p.GPUSpecID, &env.GPUSpecID)
        clearInt(&p.ExpandSystemDiskGB, &env.ExpandSystemDiskGB)
        clearString(&p.GPUSpecUUID, &env.GPUSpecUUID)
    } else {
        clearInt(&p.ExpandDataDiskGB, &env.ExpandDataDiskGB)
        if mode == "session" {
            clearString(&p.GPUSpecUUID, &env.GPUSpecUUID)
        }
    }
}

func (d *cloudDraft) edit(ctx context.Context, c *invocation) error {
    for d.stage < cloudDone {
        var next cloudTransition
        var err error
        switch d.stage {
        case cloudCompute:
            next, err = d.compute(ctx, c)
        case cloudCandidate:
            next, err = d.candidate(ctx, c)
        case cloudEnvironment:
            next, err = d.environment(ctx, c)
        case cloudReview:
            next, err = d.review(ctx, c)
        }
        if err != nil {
            return err
        }
        if next == cloudCancel {
            d.move(next)
            return interaction.ErrCanceled
        }
        if next == cloudBack && d.stage == cloudCompute {
            mode, err := d.selectChoice(ctx, c, "cloud_wizard_mode", []interaction.Choice{{Label: c.output.locale.Text("cloud_mode_select"), Value: "select"}, {Label: c.output.locale.Text("cloud_mode_auto"), Value: "auto"}}, d.mode)
            if err != nil {
                return err
            }
            d.mode = mode
        }
        d.move(next)
    }
    return nil
}

func (d *cloudDraft) compute(ctx context.Context, c *invocation) (cloudTransition, error) {
    l := c.output.locale
    before := d.in.Plan.Explicit
    defer func() {
        if !reflect.DeepEqual(before, d.in.Plan.Explicit) {
            d.invalidateCandidate()
        }
    }()
    if len(d.models) == 0 {
        page, err := d.catalog(ctx, c, cloud.CatalogInput{Category: "gpu", Limit: 1000})
        if err != nil {
            return cloudStay, err
        }
        d.models = page.Items
        page, err = d.catalog(ctx, c, cloud.CatalogInput{Category: "region", Limit: 1000})
        if err != nil {
            return cloudStay, err
        }
        d.regions = page.Items
        c.output.regionCatalog = page.Items
    }
    gpu := &interaction.Field{Title: l.Text("cloud_wizard_gpu"), Multiple: d.mode == "auto", Values: append([]string{}, d.plan.Requirements.GPUModels...)}
    if gpu.Multiple {
        gpu.Description = l.Text("cloud_models_any")
    }
    for _, m := range d.models {
        gpu.Choices = append(gpu.Choices, interaction.Choice{Label: m.Name, Value: m.ID})
    }
    if len(gpu.Values) > 0 {
        gpu.Value = gpu.Values[0]
    }
    count := &interaction.Field{Title: l.Text("cloud_wizard_count"), Value: strconv.Itoa(d.plan.Requirements.GPUCount), Validate: func(s string) error {
        n, e := parseCloudInteger(s)
        maximum := 64
        if d.plan.Product == "pro" {
            maximum = 8
        }
        if e != nil || n < 1 || n > maximum {
            return errors.New(l.Text("cloud_gpu_count_invalid", map[string]any{"Count": maximum}))
        }
        return nil
    }}
    regions := newRegionMenu(d.regions, d.plan.Requirements.Regions, l)
    region := &interaction.Field{Title: l.Text("cloud_wizard_regions"), Description: l.Text("cloud_regions_any"), Multiple: true, Values: regions.selected, Choices: regions.choices}
    if regions.defaulted {
        region.Description = l.Text("cloud_regions_default")
    }
    advanced := &interaction.Field{Title: l.Text("cloud_wizard_advanced"), Choices: []interaction.Choice{{Label: l.Text("cloud_defaults_keep"), Value: "no"}, {Label: l.Text("cloud_conditions_edit"), Value: "yes"}}, Value: "no"}
    nav := d.navField(c)
    fields := []*interaction.Field{gpu, count}
    if len(region.Choices) > 0 && d.in.CloneFrom == nil {
        fields = append(fields, region)
    }
    fields = append(fields, advanced, nav)
    if err := d.page(ctx, c, "cloud_step_compute", fields...); err != nil {
        return cloudStay, err
    }
    if nav.Value == "cancel" {
        return cloudCancel, nil
    }
    back := nav.Value == "back"
    models := gpu.Values
    if !gpu.Multiple {
        models = []string{gpu.Value}
    }
    if len(models) == 0 && !gpu.Multiple {
        return cloudStay, fault.Invalid("--gpu-model", "missing_argument", "cloud_model_required")
    }
    n, _ := parseCloudInteger(count.Value)
    d.in.Plan.Explicit.GPUModels = append([]string{}, models...)
    d.in.Plan.Explicit.GPUCount = &n
    regionIDs := regions.expand(region.Values)
    if d.in.CloneFrom != nil {
        regionIDs = append([]string{}, d.plan.Requirements.Regions...)
    }
    d.in.Plan.Explicit.Regions = regionIDs
    d.plan.Requirements.GPUModels, d.plan.Requirements.GPUCount, d.plan.Requirements.Regions = models, n, regionIDs
    if back {
        return cloudBack, nil
    }
    if advanced.Value == "yes" {
        values := []*int{&d.plan.Requirements.MinGPUMemoryGiB, &d.plan.Requirements.MinCPUs, &d.plan.Requirements.MinMemoryGiB, &d.plan.Requirements.MinDiskGiB}
        titles := []string{"cloud_wizard_gpu_memory", "cloud_wizard_cpus", "cloud_wizard_memory", "cloud_wizard_disk"}
        fields := []*interaction.Field{}
        for i, v := range values {
            fields = append(fields, &interaction.Field{Title: l.Text(titles[i]), Value: strconv.Itoa(*v), Validate: func(s string) error {
                n, e := parseCloudInteger(s)
                if e != nil || n < 0 {
                    return errors.New(l.Text("cloud_number_invalid"))
                }
                return nil
            }})
        }
        if err := d.page(ctx, c, "cloud_step_compute", fields...); err != nil {
            return cloudStay, err
        }
        for i, f := range fields {
            *values[i], _ = parseCloudInteger(f.Value)
        }
        d.in.Plan.Explicit.MinGPUMemoryGiB = cloudPointer(*values[0])
        d.in.Plan.Explicit.MinCPUs = cloudPointer(*values[1])
        d.in.Plan.Explicit.MinMemoryGiB = cloudPointer(*values[2])
        d.in.Plan.Explicit.MinDiskGiB = cloudPointer(*values[3])
    }
    return cloudNext, nil
}

func (d *cloudDraft) candidate(ctx context.Context, c *invocation) (cloudTransition, error) {
    l := c.output.locale
    if d.mode == "auto" {
        cap := ""
        if d.plan.Requirements.MaxComputeHourlyPrice != nil {
            cap = d.plan.Requirements.MaxComputeHourlyPrice.Amount
        }
        price := &interaction.Field{Title: l.Text("cloud_wizard_price"), Value: cap, Validate: func(s string) error {
            if _, e := cloud.ParseMoney(s, "CNY"); e != nil {
                return errors.New(l.Text("cloud_budget_required"))
            }
            return nil
        }}
        waiting := "continuous"
        if d.in.Plan.Explicit.WaitTimeout != nil && *d.in.Plan.Explicit.WaitTimeout != "0s" {
            waiting = "bounded"
        }
        if d.plan.Clone != nil && !d.plan.Continuous {
            waiting = "bounded"
        }
        wait := &interaction.Field{Title: l.Text("cloud_wizard_wait"), Value: waiting, Choices: []interaction.Choice{{Label: l.Text("cloud_wait_continuous"), Value: "continuous"}, {Label: l.Text("cloud_wait_bounded"), Value: "bounded"}}}
        nav := d.navField(c)
        if err := d.page(ctx, c, "cloud_step_candidate", price, wait, nav); err != nil {
            return cloudStay, err
        }
        if nav.Value == "cancel" {
            return cloudCancel, nil
        }
        d.in.Plan.Explicit.MaxComputeHourlyPrice = cloudPointer(price.Value)
        d.in.Plan.Explicit.Continuous = cloudPointer(wait.Value == "continuous")
        previousWait := d.in.Plan.Explicit.WaitTimeout
        d.in.Plan.Explicit.WaitTimeout = nil
        d.in.OfferID = ""
        d.plan.Requirements.MaxComputeHourlyPrice = cloudPointer(cloud.Money{Amount: price.Value, Currency: "CNY"})
        d.plan.Continuous = wait.Value == "continuous"
        if wait.Value == "bounded" {
            duration := &interaction.Field{Title: l.Text("cloud_wizard_duration"), Value: "30m", Validate: func(s string) error {
                _, e := cloud.Resolve(cloud.Ceiling{}, cloud.Profile{WaitTimeout: &s})
                if e != nil || s == "0s" {
                    return errors.New(l.Text("cloud_wait_invalid"))
                }
                return nil
            }}
            if previousWait != nil {
                duration.Value = *previousWait
            }
            if err := d.page(ctx, c, "cloud_step_candidate", duration); err != nil {
                return cloudStay, err
            }
            d.in.Plan.Explicit.WaitTimeout = &duration.Value
        }
        return cloudNavigation(nav.Value), nil
    }
    return d.pickCandidate(ctx, c)
}

func offerLabel(o cloud.Offer, unknown string) string {
    price := unknown
    if o.ComputeHourlyPrice != nil {
        price = o.ComputeHourlyPrice.Amount + " " + o.ComputeHourlyPrice.Currency + "/h"
    }
    return fmt.Sprintf("%s · %s · %s × %d · %s", price, o.ID, o.Model, o.GPUCount, o.Region)
}

func offerDetail(o cloud.Offer, l *i18n.Localizer) string {
    number := func(n *int) string {
        if n == nil {
            return "?"
        }
        return strconv.Itoa(*n)
    }
    driver, cuda := o.Driver, o.HighestCUDA
    if driver == "" {
        driver = "?"
    }
    if cuda == "" {
        cuda = "?"
    }
    return l.Text("cloud_host_details", map[string]any{"GPU": number(o.GPUMemoryGiB), "CPU": number(o.CPUs), "Memory": number(o.MemoryGiB), "Disk": number(o.DiskGiB), "Driver": driver, "CUDA": cuda})
}

func (d *cloudDraft) environment(ctx context.Context, c *invocation) (cloudTransition, error) {
    l := c.output.locale
    kind := "base"
    query := ""
    if d.plan.AutoDL.PrivateImageUUID != "" {
        kind = "private"
    }
    if v := d.in.Plan.Explicit.AutoDL.PrivateImageUUID; v != nil && *v != "" {
        kind = "private"
    }
    for {
        fields := []*interaction.Field{}
        var image *interaction.Field
        var items []cloud.CatalogItem
        if d.in.CloneFrom == nil {
            input := cloud.CatalogInput{Category: "image", Kind: kind, Query: query, Limit: 1000}
            if d.selected != nil {
                input.Vendor, input.Architecture = d.selected.Vendor, d.selected.Architecture
            }
            var page cloud.CatalogPage
            err := c.cloudBusy(ctx, l.Text("cloud_loading_images"), d.scope(c), func(work context.Context) error {
                var e error
                page, e = d.catalog(work, c, input)
                return e
            })
            if err != nil {
                return cloudStay, err
            }
            items = page.Items
            image = &interaction.Field{Title: l.Text("cloud_wizard_image"), Description: cloudCoverage(l, page.Examined, page.Complete)}
            if kind == "base" {
                image.Value = d.plan.AutoDL.Image
                if v := d.in.Plan.Explicit.AutoDL.Image; v != nil {
                    image.Value = *v
                }
            } else {
                image.Value = d.plan.AutoDL.PrivateImageUUID
                if v := d.in.Plan.Explicit.AutoDL.PrivateImageUUID; v != nil {
                    image.Value = *v
                }
            }
            for _, item := range items {
                label := item.Name
                if item.Vendor == "" || item.Architecture == "" || item.CUDA == "" {
                    label += " · " + l.Text("cloud_compatibility_unknown")
                }
                image.Choices = append(image.Choices, interaction.Choice{Label: label, Value: item.ID})
            }
            if len(items) > 0 {
                fields = append(fields, image)
            }
        }
        disk := d.plan.AutoDL.ExpandDataDiskGB
        if d.plan.Product == "pro" {
            disk = d.plan.AutoDL.ExpandSystemDiskGB
        }
        if v := d.in.Plan.Explicit.AutoDL.ExpandDataDiskGB; v != nil && d.plan.Product == "standard" {
            disk = *v
        }
        if v := d.in.Plan.Explicit.AutoDL.ExpandSystemDiskGB; v != nil && d.plan.Product == "pro" {
            disk = *v
        }
        expansion := &interaction.Field{Title: l.Text("cloud_wizard_expand"), Description: l.Text("cloud_expand_hint"), Value: strconv.Itoa(disk), Validate: func(s string) error {
            n, e := parseCloudInteger(s)
            if e != nil || n < 0 || d.plan.Clone != nil && n < d.plan.Clone.ExpandDataDiskGB {
                return errors.New(l.Text("cloud_number_invalid"))
            }
            return nil
        }}
        capValue := ""
        if d.selected != nil && d.selected.StorageDailyPrice != nil {
            capValue = d.selected.StorageDailyPrice.Amount
        }
        if d.plan.Requirements.MaxStorageDailyPrice != nil {
            capValue = d.plan.Requirements.MaxStorageDailyPrice.Amount
        }
        if v := d.in.Plan.Explicit.MaxStorageDailyPrice; v != nil {
            capValue = *v
        }
        storage := &interaction.Field{Title: l.Text("cloud_wizard_storage_price"), Value: capValue, Validate: func(s string) error {
            n, _ := parseCloudInteger(expansion.Value)
            if s == "" && n == 0 {
                return nil
            }
            if _, e := cloud.ParseMoney(s, "CNY"); e != nil {
                return errors.New(l.Text("cloud_storage_budget_required"))
            }
            return nil
        }}
        nav := d.navField(c)
        if d.in.CloneFrom == nil {
            other := "private"
            if kind == "private" {
                other = "base"
            }
            nav.Choices = append(nav.Choices, interaction.Choice{Label: l.Text("cloud_image_switch_" + other), Value: other}, interaction.Choice{Label: l.Text("cloud_candidate_refresh"), Value: "refresh"}, interaction.Choice{Label: l.Text("cloud_image_search"), Value: "search"})
            if len(items) == 0 {
                nav.Value = "back"
            }
        }
        fields = append(fields, expansion, storage, nav)
        if err := d.page(ctx, c, "cloud_step_environment", fields...); err != nil {
            return cloudStay, err
        }
        if nav.Value == "cancel" {
            return cloudCancel, nil
        }
        n, _ := parseCloudInteger(expansion.Value)
        if d.plan.Product == "standard" {
            d.in.Plan.Explicit.AutoDL.ExpandDataDiskGB = &n
        } else {
            d.in.Plan.Explicit.AutoDL.ExpandSystemDiskGB = &n
        }
        d.in.Plan.Explicit.MaxStorageDailyPrice = &storage.Value
        if image != nil && len(items) > 0 {
            d.in.Plan.Explicit.AutoDL.Image = cloudPointer("")
            d.in.Plan.Explicit.AutoDL.PrivateImageUUID = cloudPointer("")
            if kind == "base" {
                d.in.Plan.Explicit.AutoDL.Image = &image.Value
            } else {
                d.in.Plan.Explicit.AutoDL.PrivateImageUUID = &image.Value
            }
            for _, item := range items {
                if item.ID == image.Value {
                    d.in.Plan.ImageName = item.Name
                    d.in.Plan.Compatibility = &cloud.ImageCompatibility{Vendor: item.Vendor, Architecture: item.Architecture, CUDA: item.CUDA}
                    if d.plan.Product == "pro" && !d.cudaExplicit {
                        parts := strings.Split(item.CUDA, ".")
                        if len(parts) == 2 {
                            if n, e := strconv.Atoi(parts[0] + parts[1]); e == nil {
                                d.in.Plan.Explicit.AutoDL.CUDAFrom = &n
                            }
                        }
                    }
                }
            }
        }
        switch nav.Value {
        case "back":
            return cloudBack, nil
        case "private", "base":
            kind = nav.Value
            query = ""
            continue
        case "refresh":
            continue
        case "search":
            value, err := c.input.Text(ctx, interaction.TextPrompt{Title: l.Text("cloud_image_refine")})
            if err != nil {
                return cloudStay, err
            }
            query = value
            continue
        }
        if d.in.CloneFrom == nil && len(items) == 0 {
            continue
        }
        return cloudNext, nil
    }
}

func (d *cloudDraft) review(ctx context.Context, c *invocation) (cloudTransition, error) {
    l := c.output.locale
    if d.selected != nil {
        page, err := d.search(ctx, c)
        if err != nil {
            return cloudStay, err
        }
        found := false
        for _, o := range page.Matches {
            if o.ID == d.selected.ID {
                selected := o
                d.selected = &selected
                found = true
                break
            }
        }
        if !found {
            fmt.Fprintln(c.output.err, l.Text("cloud_candidate_changed"))
            return cloudRetryCandidate, nil
        }
        if d.selected.ComputeHourlyPrice == nil {
            return cloudStay, fault.New("PRICE_UNVERIFIABLE", "cloud_price_unverifiable", nil)
        }
        d.in.Plan.Explicit.MaxComputeHourlyPrice = cloudPointer(d.selected.ComputeHourlyPrice.Amount)
        if d.selected.StorageDailyPrice != nil {
            d.in.Plan.Explicit.MaxStorageDailyPrice = cloudPointer(d.selected.StorageDailyPrice.Amount)
        }
    }
    if d.in.CloneFrom != nil {
        _, plan, err := c.app.Resources.CloudClonePlan(ctx, d.in)
        if err != nil {
            return cloudStay, err
        }
        if err := cloud.ValidateAcquisition(plan, "session"); err != nil {
            return cloudStay, err
        }
        d.plan = plan
    } else {
        preview, err := c.app.Resources.CloudPreview(ctx, d.in)
        if err != nil {
            return cloudStay, err
        }
        d.plan = preview.Plan
    }
    // Resolve the same real image leaf used by acquisition before requesting
    // payment authority. A changed catalog leaf still requires re-approval.
    if d.in.CloneFrom == nil {
        kind, id := "base", d.plan.AutoDL.Image
        if d.plan.AutoDL.PrivateImageUUID != "" {
            kind, id = "private", d.plan.AutoDL.PrivateImageUUID
        }
        page, err := d.catalog(ctx, c, cloud.CatalogInput{Category: "image", Kind: kind, Query: id, Limit: 1000})
        if err != nil {
            return cloudStay, err
        }
        for _, item := range page.Items {
            if item.ID == id {
                d.plan.AutoDL.Name = item.Name
                d.plan.AutoDL.Compatibility = &cloud.ImageCompatibility{Vendor: item.Vendor, Architecture: item.Architecture, CUDA: item.CUDA}
                d.in.Plan.ImageName, d.in.Plan.Compatibility = item.Name, d.plan.AutoDL.Compatibility
            }
        }
    }
    settings, err := c.app.Resources.Config.Load()
    if err != nil {
        return cloudStay, err
    }
    name := &interaction.Field{Title: l.Text("cloud_wizard_name"), Value: d.in.Name, Validate: func(s string) error {
        if !config.ValidName(s) {
            return errors.New(l.Text("cloud_name_required"))
        }
        if _, ok := settings.Targets[s]; ok {
            return errors.New(l.Text("target_exists", map[string]any{"Target": s}))
        }
        return nil
    }}
    nav := d.navField(c)
    nav.Choices = append(nav.Choices, interaction.Choice{Label: l.Text("cloud_review_command"), Value: "command"})
    if d.in.CloneFrom == nil {
        nav.Choices = append(nav.Choices, interaction.Choice{Label: l.Text("cloud_hub_scope"), Value: "scope"})
    }
    summary := ""
    for _, row := range cloudPlanRows(l, d.plan, c.output.regionCatalog) {
        if summary != "" {
            summary += "\n"
        }
        summary += row.Label + "  " + row.Value
    }
    summary += "\n\n" + l.Text("cloud_ui_notice_acquire")
    payment := &interaction.Field{Title: l.Text("cloud_ui_type_yes"), Description: summary, Validate: func(s string) error {
        if nav.Value != "next" {
            return nil
        }
        if strings.TrimSpace(s) != "yes" {
            return errors.New(l.Text("cloud_confirm_required"))
        }
        return nil
    }}
    fields := []*interaction.Field{name, nav, payment}
    if err := c.input.PageWithSummary(ctx, l.Text("cloud_step_review"), d.scope(c), fields...); err != nil {
        return cloudStay, err
    }
    d.in.Name = name.Value
    if nav.Value == "cancel" {
        return cloudCancel, nil
    }
    if nav.Value == "back" {
        return cloudBack, nil
    }
    switch nav.Value {
    case "command":
        args := cloudPlanCommand(d.in, client.CloudPreview{Target: d.in.Name, Account: *d.in.Select.Account, Plan: d.plan})
        command := shellCommand(args)
        _, err := c.input.SelectWithSummary(ctx, l.Text("cloud_review_command"), command, []interaction.Choice{{Label: l.Text("cloud_nav_back"), Value: "back"}}, "back")
        if err != nil {
            return cloudStay, err
        }
        return cloudStay, nil
    case "scope":
        changed, err := d.chooseScope(ctx, c)
        if err != nil {
            return cloudStay, err
        }
        if changed {
            d.invalidateScope()
            if err := d.setup(ctx, c); err != nil {
                return cloudStay, err
            }
        }
        return cloudStay, nil
    }
    d.rememberApproval()
    return cloudNext, nil
}

// parseCloudInteger shares the same normalization for form validation and use.
// Human input may contain surrounding whitespace or full-width IME digits.
func parseCloudInteger(value string) (int, error) {
    return strconv.Atoi(width.Narrow.String(strings.TrimSpace(value)))
}

func (d *cloudDraft) approve(ctx context.Context, c *invocation, approval client.CloudApproval) error {
    if d.approved.matches(approval) {
        return nil
    }
    return c.approveCloud(ctx, approval)
}
