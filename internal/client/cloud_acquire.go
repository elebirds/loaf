package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *Resources) resourcePlan(ctx context.Context, in CloudPlanInput, defaults ...cloud.Profile) (cloud.Plan, error) {
    _, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return cloud.Plan{}, err
    }
    if in.Profile != "" {
        _, global := c.Settings.Resources[in.Profile]
        _, project := file.Resources[in.Profile]
        if !global && !project {
            return cloud.Plan{}, fault.New("BAD_CONFIG", "cloud_profile_unknown", nil)
        }
    }
    layers := append(defaults, c.Settings.Cloud.Defaults, c.Settings.Resources["default"], c.Settings.Resources[in.Profile], file.Resources["default"], file.Resources[in.Profile], in.Explicit)
    return cloud.Resolve(c.Settings.Cloud.Safety, layers...)
}

func (c *Resources) searchProvider(ctx context.Context, in CloudSearchInput) (cloud.Provider, cloud.Plan, error) {
    if in.Limit < 1 || in.Limit > 1000 {
        return nil, cloud.Plan{}, cloudConfigError()
    }
    plan, err := c.resourcePlan(ctx, in.Plan)
    if err != nil {
        return nil, plan, err
    }
    name, a, err := c.selectAccount(in.Select)
    if err != nil {
        return nil, plan, err
    }
    plan.Product = selectedProduct(in.Select, a, plan.Product)
    provider, err := c.Cloud.Open(ctx, name, a, plan.Product)
    return provider, plan, cloud.Failure(err)
}

func (c *Resources) acquisitionPlan(ctx context.Context, in CloudAcquireInput) (string, cloud.Account, cloud.Plan, error) {
    plan, err := c.resourcePlan(ctx, in.Plan)
    if err != nil {
        return "", cloud.Account{}, plan, err
    }
    if in.CloneFrom == nil {
        name, a, err := c.selectAccount(in.Select)
        plan.Product = selectedProduct(in.Select, a, plan.Product)
        return name, a, plan, err
    }
    flags := in.Plan.Explicit.AutoDL
    if *in.CloneFrom == "" || in.AllowUnquoted || flags.Image != nil || flags.PrivateImageUUID != nil || flags.GPUSpecUUID != nil || flags.GPUSpecID != nil || flags.CUDAFrom != nil || flags.ExpandSystemDiskGB != nil {
        return "", cloud.Account{}, plan, fault.New("BAD_CONFIG", "cloud_clone_options", nil)
    }
    if target, ok := c.Settings.Targets[*in.CloneFrom]; ok && target.Cloud == nil {
        return "", cloud.Account{}, plan, fault.New("BAD_CONFIG", "cloud_clone_options", nil)
    }
    selection := in.Select
    if _, ok := c.Settings.Targets[*in.CloneFrom]; !ok && selection.Product == nil && plan.Product != "" {
        selection.Product = &plan.Product
    }
    sourceBinding, err := c.binding(selection, *in.CloneFrom)
    if err != nil {
        return "", cloud.Account{}, plan, err
    }
    if plan.Product != "" && plan.Product != sourceBinding.Ref.Product {
        return "", cloud.Account{}, plan, fault.New("BAD_CONFIG", "cloud_product_mismatch", nil)
    }
    name, a := sourceBinding.Ref.Account, sourceBinding.Settings
    if sourceBinding.Ref.Product != "standard" || a.Credential.Mode != "session" {
        return name, a, plan, fault.New("UNSUPPORTED_CAPABILITY", "cloud_clone_unsupported", nil)
    }
    p, err := c.Cloud.Open(ctx, name, a, sourceBinding.Ref.Product)
    if err != nil {
        return name, a, plan, cloud.Failure(err)
    }
    cloner, ok := p.(cloud.Cloner)
    if !ok {
        return name, a, plan, fault.New("UNSUPPORTED_CAPABILITY", "cloud_clone_unsupported", nil)
    }
    source, err := cloner.CloneSource(ctx, sourceBinding.Ref)
    if err != nil {
        return name, a, plan, cloud.Failure(err)
    }
    wait := "30m"
    plan, err = c.resourcePlan(ctx, in.Plan, cloud.Profile{WaitTimeout: &wait, AutoDL: cloud.AutoDLProfile{ExpandDataDiskGB: &source.ExpandDataDiskGB}})
    if err != nil {
        return name, a, plan, err
    }
    plan.Product = sourceBinding.Ref.Product
    plan, err = cloud.PrepareClone(plan, source)
    return name, a, plan, err
}

// CloudAcquire coordinates authorization, durable allocation, binding and readiness.
// A failed readiness check keeps the same resource and receipt; it never allocates a replacement.
func (c *Resources) CloudAcquire(ctx context.Context, in CloudAcquireInput, approve ApproveCloud, emit func(cloud.Event) error) (AcquisitionResult, error) {
    result := AcquisitionResult{}
    if !config.ValidName(in.Name) || !config.ValidRemotePath(in.RemoteBase) || in.ReadyTimeout < 0 || in.ReadyTimeout > 10*time.Minute || in.Limit < 1 || in.Limit > 1000 {
        return result, cloudConfigError()
    }
    checkTarget := func() error {
        current, err := c.Config.Load()
        if err != nil {
            return err
        }
        if _, ok := current.Targets[in.Name]; ok {
            return fault.New("BAD_CONFIG", "target_exists", map[string]any{"Target": in.Name})
        }
        return nil
    }
    if err := checkTarget(); err != nil {
        return result, err
    }
    name, a, plan, err := c.acquisitionPlan(ctx, in)
    if err != nil {
        return result, err
    }
    result.Plan = &plan
    if err = approveCloud(ctx, in.Yes, approve, CloudApproval{Operation: "acquire", Plan: &plan}); err != nil {
        return result, err
    }
    var observation *cloud.SearchResult
    observe := func(event cloud.Event) error {
        if event.Observation != nil {
            observation = event.Observation
        }
        if emit != nil {
            return emit(event)
        }
        return nil
    }
    r, err := c.Cloud.Acquire(ctx, cloud.AcquireInput{Account: name, Settings: a, Target: in.Name, RemoteBase: in.RemoteBase, Plan: plan, Limit: in.Limit, AllowUnquoted: in.AllowUnquoted, CheckTarget: checkTarget}, observe)
    result.Acquisition = &CloudAcquisition{Receipt: r, Observation: observation}
    if err != nil {
        return result, err
    }
    if r.Instance == nil {
        return result, fault.New("CLOUD_UNCONFIRMED", "cloud_unconfirmed", nil)
    }
    b := cloud.Binding{Ref: *r.Instance, Settings: a}
    bound, err := c.bindCloud(ctx, in.Name, b, in.RemoteBase, false, in.ReadyTimeout, plan.Clone)
    bound.Request = &r
    bound.Observation = observation
    result.Binding = &bound
    if err != nil {
        return result, err
    }
    if err = c.Cloud.MarkReady(ctx, r.ID, b.Ref); err != nil {
        return result, err
    }
    r.Status = "ready"
    result.Acquisition.Receipt = r
    return result, nil
}
