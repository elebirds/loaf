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

func (c *invocation) cloud(ctx context.Context, path string) (any, error) {
    g, p, r := c.grammar.Cloud, c.output, c.app.Resources
    selectInput := client.CloudSelect{Account: g.Account, Product: g.Product}
    if g.Product != nil && !cloud.ValidProduct(*g.Product) {
        return nil, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    var approval client.ApproveCloud
    if !c.policy.Batch {
        approval = c.approveCloud
    }
    switch path {
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
        return complete(r.CloudInfo(ctx, client.CloudInstanceInput{Select: selectInput, ID: g.Info.ID}))
    case "cloud credentials":
        return complete(r.CloudCredentials(ctx, client.CloudInstanceInput{Select: selectInput, ID: g.Credentials.ID, ShowPassword: g.Credentials.ShowPassword}))
    case "cloud start", "cloud stop":
        v := g.Stop
        if path == "cloud start" {
            v = g.Start
        }
        result, err := r.CloudPower(ctx, client.CloudPowerInput{Select: selectInput, ID: v.ID, Start: path == "cloud start", Yes: v.Yes, Wait: v.Wait}, approval)
        if result.Receipt != nil {
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
        v := g.Acquire
        plan, err := v.ResourceFlags.plan(g.Product)
        if err != nil {
            return nil, err
        }
        result, err := r.CloudAcquire(ctx, client.CloudAcquireInput{Select: selectInput, Plan: plan, Name: v.Name, Limit: v.Limit, CloneFrom: v.CloneFrom, Yes: v.Yes, AllowUnquoted: v.AllowUnquoted, RemoteBase: v.RemoteDir, ReadyTimeout: v.ReadyTimeout, Notify: v.Notify}, approval, func(event cloud.Event) error {
            if p.json {
                return nil
            }
            return c.cloudEvent(event)
        })
        if result.Binding != nil {
            return *result.Binding, err
        }
        if result.Acquisition != nil {
            return *result.Acquisition, err
        }
        if result.Plan != nil {
            return *result.Plan, err
        }
        return nil, err
    case "cloud bind":
        v := g.Bind
        return r.CloudBind(ctx, client.CloudBindInput{Select: selectInput, Name: v.Name, Instance: v.Instance, Request: v.Request, RemoteBase: v.RemoteDir, Replace: v.Replace, ReadyTimeout: v.ReadyTimeout})
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
    fmt.Fprintln(p.err, p.locale.Text("cloud_approval_identity", map[string]any{"Account": approval.Account, "Product": approval.Product, "Target": approval.Target, "ID": approval.InstanceID, "Request": approval.RequestID}))
    prompt := p.locale.Text("cloud_power_confirm", map[string]any{"Value": approval.Operation, "ID": approval.InstanceID})
    if approval.Operation == "no-instance" {
        prompt = p.locale.Text("cloud_no_instance_confirm")
    }
    if approval.Plan != nil {
        printCloud(p.err, *approval.Plan, p.locale)
        prompt = p.locale.Text("cloud_acquire_confirm")
        if approval.Plan.Clone != nil {
            prompt = p.locale.Text("cloud_clone_confirm", map[string]any{"ID": approval.Plan.Clone.Instance.ID, "Region": approval.Plan.Clone.Region})
        }
    }
    yes, err := c.input.Confirm(ctx, interaction.Confirmation{Title: prompt, Kind: interaction.ConfirmTypedYes})
    if err != nil {
        return err
    }
    if !yes {
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
    v, g, p := c.grammar.Cloud.Login, c.grammar.Cloud, c.output
    prompts := client.LoginPrompts{}
    plain := c.input.Plain()
    if !c.policy.Batch {
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
    return complete(c.app.Resources.Login(ctx, client.LoginInput{Name: v.Name, Username: v.Username, PhoneArea: v.PhoneArea, CredentialStore: v.CredentialStore, Product: g.Product, PasswordStdin: v.PasswordStdin, Prompt: v.Prompt, Verify: v.Verify, Remember: v.RememberPassword, Replace: v.Replace, Default: v.Default}, c.policy, prompts))
}

func (f ResourceFlags) plan(product *string) (client.CloudPlanInput, error) {
    profile := cloud.Profile{Product: product, GPUVendor: f.GPUVendor, GPUCount: f.GPUCount, MinCPUs: f.MinCPUs, MaxComputeHourlyPrice: f.MaxComputeHourlyPrice, Currency: f.Currency, WaitTimeout: f.Wait, PollInterval: f.PollInterval, AutoDL: cloud.AutoDLProfile{Image: f.Image, PrivateImageUUID: f.PrivateImage, GPUSpecID: f.GPUSpecID, GPUSpecUUID: f.GPUSpec, CUDAFrom: f.CUDAFrom, ExpandSystemDiskGB: f.ExpandSystemDisk, ExpandDataDiskGB: f.ExpandDataDisk}}
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
