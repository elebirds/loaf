package cli

import (
    "context"
    "errors"
    "fmt"
    "io"
    "strconv"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *invocation) cloud(ctx context.Context, path string) (data any, err error) {
    defer func() {
        if actionErr := c.cloudNavigation(path, data, err); actionErr != nil {
            err = errors.Join(err, actionErr)
        }
    }()
    g, p, r := c.grammar.Cloud, c.output, c.app.Resources
    selectInput := client.CloudSelect{Account: g.Account, Product: g.Product}
    if g.Product != nil && !cloud.ValidProduct(*g.Product) {
        return nil, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    var approval client.ApproveCloud
    if c.interactiveInput() {
        approval = c.approveCloud
    }
    switch path {
    case "cloud profile save":
        v := g.Profile.Save
        plan, e := v.ResourceFlags.plan(g.Product)
        if e != nil {
            return nil, e
        }
        plan.Explicit.Continuous = v.Continuous
        row, e := r.SaveResolvedCloudProfile(ctx, v.Name, plan, selectInput, v.Replace)
        return cloudProfilesResult{Profiles: []client.CloudProfile{row}}, e
    case "cloud profile remove":
        v := g.Profile.Remove
        if !v.Yes {
            if !c.interactiveInput() {
                return nil, fault.Invalid("--yes", "missing_argument", "cloud_confirm_required")
            }
            accepted, e := c.input.Confirm(ctx, interaction.Confirmation{Title: p.locale.Text("cloud_profile_remove_confirm", map[string]any{"Target": v.Name}), Kind: interaction.ConfirmDefaultNo, AcceptLabel: p.locale.Text("yes"), RejectLabel: p.locale.Text("no")})
            if e != nil {
                return nil, e
            }
            if !accepted {
                return nil, interaction.ErrCanceled
            }
        }
        rows, e := r.RemoveCloudProfile(ctx, v.Name)
        return cloudProfilesResult{Profiles: rows}, e
    case "cloud account use":
        return complete(r.UseCloudAccount(ctx, g.AccountCommands.Use.Name, g.Product))
    case "cloud profile", "cloud profile list", "cloud profile show":
        name := ""
        if path == "cloud profile show" {
            name = g.Profile.Show.Name
        }
        rows, e := r.CloudProfiles(ctx, name)
        return cloudProfilesResult{Profiles: rows}, e
    case "cloud catalog gpu", "cloud catalog region", "cloud catalog image":
        v := g.Catalog.GPU
        category := "gpu"
        kind := ""
        if path == "cloud catalog region" {
            v = g.Catalog.Region
            category = "region"
        }
        if path == "cloud catalog image" {
            v = g.Catalog.Image.catalogQuery
            kind = g.Catalog.Image.Kind
            category = "image"
        }
        return r.CloudCatalog(ctx, selectInput, cloud.CatalogInput{Category: category, Kind: kind, Query: v.Query, Limit: v.Limit})
    case "cloud account", "cloud account list":
        return complete(r.CloudAccounts())
    case "cloud account add":
        v := g.AccountCommands.Add
        product := "standard"
        if g.Product != nil {
            product = *g.Product
        }
        account := cloud.Account{Provider: "autodl", DefaultProduct: product, Credential: cloud.CredentialRef{Mode: v.Auth, Env: v.TokenEnv}, AppVersion: v.AppVersion}
        return complete(r.AddCloudAccount(ctx, client.CloudAccountInput{Name: v.Name, Account: account, Replace: v.Replace, Default: v.Default}))
    case "cloud account remove":
        return complete(r.RemoveCloudAccount(ctx, g.AccountCommands.Remove.Name))
    case "cloud login":
        return c.login(ctx)
    case "cloud logout":
        return complete(r.Logout(ctx, g.Logout.Name))
    case "cloud list":
        return complete(r.CloudList(ctx, selectInput, g.List.Limit))
    case "cloud info":
        id, e := cloudInstanceID(g.Info.ID)
        if e != nil {
            return nil, e
        }
        return complete(r.CloudInfo(ctx, client.CloudInstanceInput{Select: selectInput, ID: id}))
    case "cloud credentials":
        return complete(r.CloudCredentials(ctx, client.CloudInstanceInput{Select: selectInput, ID: g.Credentials.ID, ShowPassword: g.Credentials.ShowPassword}))
    case "cloud start", "cloud stop":
        v := g.Stop
        if path == "cloud start" {
            v = g.Start
        }
        id, e := cloudInstanceID(v.ID)
        if e != nil {
            return nil, e
        }
        runCtx, cancel := context.WithCancel(ctx)
        defer cancel()
        feedback := &cloudFeedback{call: c, mode: v.Progress, cancel: cancel, ctx: runCtx}
        defer func() {
            if e := feedback.close(); e != nil && err == nil {
                err = e
            }
        }()
        result, err := r.CloudPower(runCtx, client.CloudPowerInput{Select: selectInput, ID: id, Start: path == "cloud start", Yes: v.Yes, Wait: v.Wait, Progress: feedback.emit}, approval)
        if result.Receipt != nil && (result.Receipt.ID != "" || result.Receipt.Instance != nil) {
            return *result.Receipt, err
        }
        if result.Reference.ID != "" {
            return result.Reference, err
        }
        return nil, err
    case "cloud search", "cloud watch":
        v := g.Search
        if path == "cloud watch" {
            v = g.Watch
        }
        plan, err := v.ResourceFlags.plan(g.Product)
        if err != nil {
            return nil, err
        }
        input := client.CloudSearchInput{Select: selectInput, Plan: plan, Limit: v.Limit}
        if path == "cloud search" {
            return r.CloudSearch(ctx, input)
        }
        result, err := r.CloudWatch(ctx, input, c.cloudEvent)
        if p.json {
            if err != nil {
                return nil, err
            }
            return struct {
                Type        string             `json:"type"`
                Observation cloud.SearchResult `json:"observation"`
            }{Type: "end", Observation: result}, nil
        }
        return result, err
    case "cloud acquire":
        return c.acquireCloud(ctx)
    case "cloud bind":
        v := g.Bind
        if v.Instance == "" && v.Request == "" {
            return nil, fault.Invalid("instance/--request", "missing_argument", "cloud_instance_required")
        }
        runCtx, cancel := context.WithCancel(ctx)
        defer cancel()
        feedback := &cloudFeedback{call: c, mode: v.Progress, cancel: cancel, ctx: runCtx}
        defer func() {
            if e := feedback.close(); e != nil && err == nil {
                err = e
            }
        }()
        result, bindErr := r.CloudBind(runCtx, client.CloudBindInput{Select: selectInput, Name: v.Name, Instance: v.Instance, Request: v.Request, RemoteBase: v.RemoteDir, Replace: v.Replace, ReadyTimeout: v.ReadyTimeout, Progress: feedback.emit})
        if closeErr := feedback.close(); closeErr != nil {
            return result, closeErr
        }
        return result, bindErr
    case "cloud requests":
        if g.Requests.ID != "" {
            return r.CloudRequest(ctx, selectInput, g.Requests.ID)
        }
        return r.CloudRequests(ctx, selectInput)
    case "cloud reconcile":
        v := g.Reconcile
        return r.CloudReconcile(ctx, client.CloudReconcileInput{Select: selectInput, Request: v.ID, Instance: v.Instance, NoInstance: v.NoInstance, Yes: v.Yes}, approval)
    }
    return nil, parseFailure()
}

func (c *invocation) approveCloud(ctx context.Context, approval client.CloudApproval) error {
    p := c.output
    target := approval.Target
    if target == approval.InstanceID {
        target = ""
    }
    d := interaction.Document{Title: p.locale.Text("cloud_ui_approve_" + approval.Operation), Context: cloudScope(p.locale, approval.Account, approval.Product, target)}
    if p.cloudWizard && approval.Operation == "acquire" {
        d.Context = withCloudRoadmap(d.Context, p.locale, 4)
    }
    if approval.InstanceID != "" {
        d.Rows = append(d.Rows, interaction.Row{Label: p.locale.Text("cloud_ui_label_instance"), Value: approval.InstanceID})
    }
    if approval.RequestID != "" {
        d.Rows = append(d.Rows, interaction.Row{Label: p.locale.Text("cloud_ui_label_request"), Value: approval.RequestID})
    }
    d.Body = p.locale.Text("cloud_ui_notice_" + approval.Operation)
    if approval.Plan != nil {
        d.Rows = cloudPlanRows(p.locale, *approval.Plan, p.regionCatalog)
        if approval.Plan.Clone != nil {
            d.Body = p.locale.Text("cloud_ui_clone_notice") + "\n" + d.Body
        }
    }
    yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: p.locale.Text("cloud_ui_type_yes"), Summary: &d, Kind: interaction.ConfirmTypedYes})
    if err != nil {
        if errors.Is(err, context.Canceled) || errors.Is(err, interaction.ErrCanceled) {
            p.cloudDeclined = true
        }
        return err
    }
    if !yes {
        p.cloudDeclined = true
        return fault.New("BAD_CONFIG", "cloud_confirm_required", nil)
    }
    return nil
}

func (c *invocation) cloudEvent(event cloud.Event) error {
    if c.output.json {
        return c.output.stream(event)
    }
    if event.Observation != nil {
        printCloud(c.output.err, *event.Observation, c.output.locale)
    }
    if event.RequestID != "" {
        fmt.Fprintln(c.output.err, event.RequestID)
    }
    return nil
}

func (c *invocation) login(ctx context.Context) (any, error) {
    v, g := c.grammar.Cloud.Login, c.grammar.Cloud
    return c.loginRequest(ctx, client.LoginInput{Name: v.Name, Username: v.Username, PhoneArea: v.PhoneArea, CredentialStore: v.CredentialStore, Product: g.Product, PasswordStdin: v.PasswordStdin, Prompt: v.Prompt, Verify: v.Verify, Remember: v.RememberPassword, Replace: v.Replace, Default: v.Default})
}

// loginRequest borrows the same native input session for an explicit typed login.
// Acquisition can authenticate in place without changing its command grammar.
func (c *invocation) loginRequest(ctx context.Context, in client.LoginInput) (any, error) {
    p := c.output
    prompts := client.LoginPrompts{}
    plain := c.input.Plain()
    if !c.policy.Batch && (c.interactiveInput() || in.Prompt || in.Verify) {
        prompts.Username = func(ctx context.Context) (string, error) {
            value, err := plain.Text(ctx, interaction.TextPrompt{Title: p.locale.Text("cloud_username_prompt"), MaxBytes: 4096})
            return value, loginInputError(err, "cloud_username_required")
        }
        prompts.Password = func(ctx context.Context) ([]byte, error) {
            value, err := plain.Secret(ctx, interaction.SecretPrompt{Title: p.locale.Text("cloud_password_prompt"), MaxBytes: 4096})
            return value, loginInputError(err, "cloud_password_required")
        }
        prompts.Verification = func(ctx context.Context) ([]byte, error) {
            value, err := plain.Secret(ctx, interaction.SecretPrompt{Title: p.locale.Text("cloud_verification_prompt"), MaxBytes: 4096})
            return value, loginInputError(err, "cloud_verification_required")
        }
    }
    prompts.StdinPassword = func(ctx context.Context) ([]byte, error) {
        value, err := c.input.ReadSecretLine(ctx, 4096)
        return value, loginInputError(err, "cloud_password_required")
    }
    return complete(c.app.Resources.Login(ctx, in, c.policy, prompts))
}

func (f ResourceFlags) plan(product *string) (client.CloudPlanInput, error) {
    profile := cloud.Profile{Product: product, GPUVendor: f.GPUVendor, GPUCount: f.GPUCount, MinCPUs: f.MinCPUs, MaxComputeHourlyPrice: f.MaxComputeHourlyPrice, Currency: f.Currency, WaitTimeout: f.Wait, PollInterval: f.PollInterval, AutoDL: cloud.AutoDLProfile{Image: f.Image, PrivateImageUUID: f.PrivateImage, GPUSpecID: f.GPUSpecID, GPUSpecUUID: f.GPUSpec, CUDAFrom: f.CUDAFrom, ExpandSystemDiskGB: f.ExpandSystemDisk, ExpandDataDiskGB: f.ExpandDataDisk}}
    profile.MaxStorageDailyPrice = f.MaxStorageDailyPrice
    if f.GPUModel.Present {
        profile.GPUModels = f.GPUModel.Values
    }
    if f.Region.Present {
        profile.Regions = f.Region.Values
    }
    for _, field := range []struct {
        raw  *string
        dest **int
    }{{f.MinGPUMemory, &profile.MinGPUMemoryGiB}, {f.MinMemory, &profile.MinMemoryGiB}, {f.MinDisk, &profile.MinDiskGiB}} {
        if field.raw == nil {
            continue
        }
        n, err := strconv.Atoi(strings.TrimSuffix(*field.raw, "GiB"))
        if err != nil || n < 0 {
            return client.CloudPlanInput{}, fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        *field.dest = &n
    }
    return client.CloudPlanInput{Profile: f.Profile, Explicit: profile}, nil
}

func loginInputError(err error, message string) error {
    if errors.Is(err, interaction.ErrEmptySecret) || errors.Is(err, interaction.ErrInputTooLong) || errors.Is(err, interaction.ErrTerminalRequired) || errors.Is(err, io.EOF) {
        return fault.New("BAD_CONFIG", message, nil)
    }
    return err
}
