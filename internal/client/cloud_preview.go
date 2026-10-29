package client

import (
    "context"
    "reflect"
    "sort"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// CloudPreview is a local requirement plan, not a reservation or a live quote.
type CloudPreview struct {
    Account       string            `json:"account"`
    AuthMode      string            `json:"auth_mode,omitempty"`
    Target        string            `json:"target"`
    Plan          cloud.Plan        `json:"plan"`
    Sources       map[string]string `json:"sources"`
    PendingChecks []string          `json:"pending_checks"`
    CloneSource   *cloud.Ref        `json:"clone_source,omitempty"`
    Inherited     []string          `json:"inherited_pending,omitempty"`
}

// CloudPreview validates local input without credentials, network, IDs or writes.
func (c *Resources) CloudPreview(ctx context.Context, in CloudAcquireInput) (CloudPreview, error) {
    out := CloudPreview{Target: in.Name, Sources: map[string]string{}, PendingChecks: []string{"stock", "quote", "image_availability", "ssh"}}
    if err := ctx.Err(); err != nil {
        return out, err
    }
    if !config.ValidName(in.Name) || !config.ValidRemotePath(in.RemoteBase) || in.ReadyTimeout < 0 || in.ReadyTimeout > 10*time.Minute || in.Limit < 1 || in.Limit > 1000 {
        return out, cloudConfigError()
    }
    if in.Plan.Explicit.Continuous != nil && *in.Plan.Explicit.Continuous && in.Plan.Explicit.WaitTimeout != nil {
        return out, fault.Invalid("--continuous", "conflicting_arguments", "cloud_continuous_wait")
    }
    if _, exists := c.Settings.Targets[in.Name]; exists {
        return out, fault.New("BAD_CONFIG", "target_exists", map[string]any{"Target": in.Name})
    }
    defaults := []cloud.Profile{}
    if in.CloneFrom != nil {
        if err := validateCloneOptions(in); err != nil {
            return out, err
        }
        wait := "30m"
        defaults = append(defaults, cloud.Profile{WaitTimeout: &wait})
    }
    plan, err := c.resourcePlan(ctx, in.Plan, defaults...)
    if err != nil {
        return out, err
    }
    name := c.Settings.Cloud.DefaultAccount
    if in.Select.Account != nil {
        name = *in.Select.Account
    }
    a, ok := c.Settings.Cloud.Accounts[name]
    if in.CloneFrom != nil {
        if target, exists := c.Settings.Targets[*in.CloneFrom]; exists && target.Cloud == nil {
            return out, fault.Invalid("--clone-from", "conflicting_arguments", "cloud_clone_options")
        }
        selection := in.Select
        if _, exists := c.Settings.Targets[*in.CloneFrom]; !exists && selection.Product == nil && plan.Product != "" {
            selection.Product = &plan.Product
        }
        b, err := c.localBinding(selection, *in.CloneFrom)
        if err != nil {
            return out, err
        }
        if plan.Product != "" && plan.Product != b.Ref.Product {
            return out, fault.New("BAD_CONFIG", "cloud_product_mismatch", nil)
        }
        if b.Ref.Product != "standard" || b.Settings.Credential.Mode != "session" {
            return out, fault.New("UNSUPPORTED_CAPABILITY", "cloud_clone_unsupported", nil)
        }
        name, a, ok = b.Ref.Account, b.Settings, true
        plan.Product = b.Ref.Product
        out.CloneSource = &b.Ref
        out.Inherited = []string{"regions", "autodl.image", "autodl.private_image_uuid"}
        plan.AutoDL = cloud.ImageOptions{}
        if in.Plan.Explicit.AutoDL.ExpandDataDiskGB == nil {
            out.Inherited = append(out.Inherited, "autodl.expand_data_disk_gb")
        } else {
            plan.AutoDL.ExpandDataDiskGB = *in.Plan.Explicit.AutoDL.ExpandDataDiskGB
        }
    }
    if !ok {
        return out, fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
    }
    out.Account = name
    out.AuthMode = a.Credential.Mode
    plan.Product = selectedProduct(in.Select, a, plan.Product)
    if !cloud.ValidProduct(plan.Product) {
        return out, cloudConfigError()
    }
    applyOffer(&plan, in)
    if in.CloneFrom == nil {
        plan.AutoDL.Name, plan.AutoDL.Compatibility = in.Plan.ImageName, in.Plan.Compatibility
    }
    out.Plan = plan
    _, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return out, err
    }
    out.Sources = c.resourceSources(in.Plan, file, plan)
    if in.OfferID != "" {
        if in.Plan.Explicit.WaitTimeout == nil {
            out.Sources["wait_timeout"] = "fixed offer"
        }
        if in.Plan.Explicit.Continuous == nil {
            out.Sources["continuous"] = "fixed offer"
        }
    }
    if in.Select.Product != nil {
        out.Sources["product"] = "flag --product"
    } else if plan.Product == a.DefaultProduct && out.Sources["product"] == "builtin" {
        out.Sources["product"] = "account default_product"
    }
    if in.CloneFrom != nil {
        out.PendingChecks = append(out.PendingChecks, "clone_source_stopped", "clone_region", "clone_image", "clone_disk")
        for _, key := range out.Inherited {
            out.Sources[key] = "clone_source (pending)"
        }
        return out, cloud.ValidateAcquisitionWindow(plan)
    }
    return out, cloud.ValidateAcquisition(plan, a.Credential.Mode)
}

func (c *Resources) resourceSources(in CloudPlanInput, file config.ProjectFile, plan cloud.Plan) map[string]string {
    sources := map[string]string{}
    layers := []struct {
        name string
        p    cloud.Profile
    }{
        {"user cloud.defaults", c.Settings.Cloud.Defaults},
        {"user resources.default", c.Settings.Resources["default"]},
        {"user resources." + in.Profile, c.Settings.Resources[in.Profile]},
        {"project resources.default", file.Resources["default"]},
        {"project resources." + in.Profile, file.Resources[in.Profile]},
        {"flags", in.Explicit},
    }
    profileSources(reflect.ValueOf(cloud.Profile{}), "", "builtin", sources)
    for _, l := range layers {
        profileSources(reflect.ValueOf(l.p), "", l.name, sources)
    }
    if c.Settings.Cloud.Safety.MaxComputeHourlyPrice != "" {
        ceiling, _ := cloud.ParseMoney(c.Settings.Cloud.Safety.MaxComputeHourlyPrice, "CNY")
        if plan.Requirements.MaxComputeHourlyPrice != nil && !cloud.Less(*plan.Requirements.MaxComputeHourlyPrice, ceiling) {
            sources["max_compute_hourly_price"] = "user cloud.safety"
        }
    }
    return sources
}

func profileSources(v reflect.Value, prefix, source string, out map[string]string) {
    t := v.Type()
    for i := 0; i < v.NumField(); i++ {
        f := v.Field(i)
        key := strings.Split(t.Field(i).Tag.Get("toml"), ",")[0]
        if f.Kind() == reflect.Struct {
            profileSources(f, prefix+key+".", source, out)
        } else if source == "builtin" || (f.Kind() == reflect.Pointer || f.Kind() == reflect.Slice) && !f.IsNil() {
            out[prefix+key] = source
        }
    }
}

// CloudProfile lists the available template scopes and optionally its effective plan.
type CloudProfile struct {
    Name    string            `json:"name"`
    User    bool              `json:"user"`
    Project bool              `json:"project"`
    Plan    *cloud.Plan       `json:"plan,omitempty"`
    Sources map[string]string `json:"sources,omitempty"`
}

// CloudProfiles discovers templates without reading credentials or cloud state.
func (c *Resources) CloudProfiles(ctx context.Context, name string) ([]CloudProfile, error) {
    _, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return nil, err
    }
    names := map[string]bool{}
    for n := range c.Settings.Resources {
        names[n] = true
    }
    for n := range file.Resources {
        names[n] = true
    }
    if name != "" && !names[name] {
        return nil, fault.New("BAD_CONFIG", "cloud_profile_unknown", nil)
    }
    keys := make([]string, 0, len(names))
    for n := range names {
        if name == "" || n == name {
            keys = append(keys, n)
        }
    }
    sort.Strings(keys)
    rows := []CloudProfile{}
    for _, n := range keys {
        _, u := c.Settings.Resources[n]
        _, p := file.Resources[n]
        row := CloudProfile{Name: n, User: u, Project: p}
        if name != "" {
            plan, e := c.resourcePlan(ctx, CloudPlanInput{Profile: n})
            if e != nil {
                return rows, e
            }
            row.Plan = &plan
            row.Sources = c.resourceSources(CloudPlanInput{Profile: n}, file, plan)
        }
        rows = append(rows, row)
    }
    return rows, nil
}

// UseCloudAccount changes only the local convenience selection.
func (c *Resources) UseCloudAccount(ctx context.Context, name string, product *string) (CloudAccounts, error) {
    if err := c.Config.UseCloudAccount(ctx, name, product); err != nil {
        return CloudAccounts{}, err
    }
    settings, err := c.Config.Load()
    if err != nil {
        return CloudAccounts{}, err
    }
    c.Settings = settings
    return c.CloudAccounts()
}

// SaveCloudProfile saves explicit personal resource preferences, never approval.
func (c *Resources) SaveCloudProfile(ctx context.Context, name string, p cloud.Profile, replace bool) error {
    if err := c.Config.SaveCloudProfile(ctx, name, p, replace); err != nil {
        return err
    }
    settings, err := c.Config.Load()
    if err == nil {
        c.Settings = settings
    }
    return err
}

// SaveResolvedCloudProfile resolves local conditions without opening a provider.
func (c *Resources) SaveResolvedCloudProfile(ctx context.Context, name string, in CloudPlanInput, selection CloudSelect, replace bool) (CloudProfile, error) {
    plan, err := c.resourcePlan(ctx, in)
    if err != nil {
        return CloudProfile{}, err
    }
    if selection.Product != nil {
        plan.Product = *selection.Product
    }
    if plan.Product == "" {
        account := c.Settings.Cloud.DefaultAccount
        if selection.Account != nil {
            account = *selection.Account
        }
        if a, ok := c.Settings.Cloud.Accounts[account]; ok {
            plan.Product = a.DefaultProduct
        }
    }
    if err := c.SaveCloudProfile(ctx, name, cloud.ProfileFromPlan(plan), replace); err != nil {
        return CloudProfile{}, err
    }
    rows, err := c.CloudProfiles(ctx, name)
    if err != nil {
        return CloudProfile{}, err
    }
    return rows[0], nil
}

// RemoveCloudProfile preserves project templates, credentials and bindings.
func (c *Resources) RemoveCloudProfile(ctx context.Context, name string) ([]CloudProfile, error) {
    if err := c.Config.RemoveCloudProfile(ctx, name); err != nil {
        return nil, err
    }
    settings, err := c.Config.Load()
    if err != nil {
        return nil, err
    }
    c.Settings = settings
    return c.CloudProfiles(ctx, "")
}
