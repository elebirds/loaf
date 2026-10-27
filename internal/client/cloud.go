package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
)

func cloudConfigError() error {
    return fault.New("BAD_CONFIG", "cloud_config", nil)
}

func (c *Resources) selectAccount(in CloudSelect) (string, cloud.Account, error) {
    name := c.Settings.Cloud.DefaultAccount
    if in.Account != nil {
        name = *in.Account
    }
    account, ok := c.Settings.Cloud.Accounts[name]
    if !ok {
        return name, account, fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
    }
    if in.Product != nil && !cloud.ValidProduct(*in.Product) {
        return name, account, cloudConfigError()
    }
    if c.Cloud == nil {
        return name, account, fault.New("CLOUD_FAILED", "cloud_failed", nil)
    }
    return name, account, nil
}

func selectedProduct(in CloudSelect, account cloud.Account, profile string) string {
    if in.Product != nil {
        return *in.Product
    }
    if profile != "" {
        return profile
    }
    return account.DefaultProduct
}

// CloudList observes bounded instance inventory without acquiring resources.
func (c *Resources) CloudList(ctx context.Context, in CloudSelect, limit int) (cloud.Page, error) {
    if limit < 1 || limit > 1000 {
        return cloud.Page{}, cloudConfigError()
    }
    name, a, err := c.selectAccount(in)
    if err != nil {
        return cloud.Page{}, err
    }
    p, err := c.Cloud.Open(ctx, name, a, selectedProduct(in, a, ""))
    if err != nil {
        return cloud.Page{}, cloud.Failure(err)
    }
    result, err := p.List(ctx, limit)
    return result, cloud.Failure(err)
}

// CloudInfo resolves aliases using the saved instance identity.
func (c *Resources) CloudInfo(ctx context.Context, in CloudInstanceInput) (cloud.Instance, error) {
    b, err := c.binding(in.Select, in.ID)
    if err != nil {
        return cloud.Instance{}, err
    }
    p, err := c.Cloud.Open(ctx, b.Ref.Account, b.Settings, b.Ref.Product)
    if err != nil {
        return cloud.Instance{}, cloud.Failure(err)
    }
    result, err := p.Get(ctx, b.Ref)
    return result, cloud.Failure(err)
}

// CloudCredentials reveals password material only when explicitly requested.
func (c *Resources) CloudCredentials(ctx context.Context, in CloudInstanceInput) (CloudCredentials, error) {
    b, err := c.binding(in.Select, in.ID)
    if err != nil {
        return CloudCredentials{}, err
    }
    p, err := c.Cloud.Open(ctx, b.Ref.Account, b.Settings, b.Ref.Product)
    if err != nil {
        return CloudCredentials{}, cloud.Failure(err)
    }
    conn, err := p.Connection(ctx, b.Ref)
    if err != nil {
        return CloudCredentials{}, cloud.Failure(err)
    }
    result := CloudCredentials{Instance: b.Ref, Connection: conn}
    if in.ShowPassword {
        password := conn.Password
        result.Password = &password
    }
    result.Connection.Password = ""
    return result, nil
}

func approveCloud(ctx context.Context, yes bool, approve ApproveCloud, request CloudApproval) error {
    if yes {
        return nil
    }
    if approve == nil {
        return fault.New("BAD_CONFIG", "cloud_confirm_required", nil)
    }
    return approve(ctx, request)
}

// CloudPower confirms one mutation and retains uncertain outcomes for reconciliation.
func (c *Resources) CloudPower(ctx context.Context, in CloudPowerInput, approve ApproveCloud) (PowerResult, error) {
    b, err := c.binding(in.Select, in.ID)
    result := PowerResult{Reference: b.Ref}
    if err != nil {
        return result, err
    }
    if in.Wait < 0 || in.Wait > 30*time.Minute {
        return result, cloudConfigError()
    }
    op := "stop"
    if in.Start {
        op = "start"
    }
    if err = approveCloud(ctx, in.Yes, approve, CloudApproval{Operation: op, InstanceID: b.Ref.ID}); err != nil {
        return result, err
    }
    r, err := c.Cloud.Power(ctx, b, in.Start, in.Wait)
    result.Receipt = &r
    return result, err
}

// CloudSearch computes eligible offers from a frozen requirement plan.
func (c *Resources) CloudSearch(ctx context.Context, in CloudSearchInput) (cloud.SearchResult, error) {
    p, plan, err := c.searchProvider(ctx, in)
    if err != nil {
        return cloud.SearchResult{}, err
    }
    result, err := c.Cloud.Search(ctx, p, plan, in.Limit)
    return result, cloud.Failure(err)
}

// CloudWatch observes resource changes in the foreground, without allocation.
func (c *Resources) CloudWatch(ctx context.Context, in CloudSearchInput, emit func(cloud.Event) error) (cloud.SearchResult, error) {
    p, plan, err := c.searchProvider(ctx, in)
    if err != nil {
        return cloud.SearchResult{}, err
    }
    result, err := c.Cloud.Watch(ctx, p, plan, in.Limit, emit)
    return result, cloud.Failure(err)
}

func receiptMatches(r cloud.Receipt, in CloudSelect) bool {
    product := r.Plan.Product
    if r.Instance != nil {
        product = r.Instance.Product
    }
    return (in.Account == nil || *in.Account == r.Account) && (in.Product == nil || *in.Product == product)
}

// CloudRequests lists saved recoverable intents, optionally scoped to an account/product.
func (c *Resources) CloudRequests(ctx context.Context, in CloudSelect) (CloudReceipts, error) {
    if err := ctx.Err(); err != nil {
        return CloudReceipts{}, err
    }
    if c.Cloud == nil {
        return CloudReceipts{}, fault.New("CLOUD_FAILED", "cloud_failed", nil)
    }
    rows, err := c.Cloud.Receipts.List()
    result := CloudReceipts{Requests: []cloud.Receipt{}}
    for _, r := range rows {
        if receiptMatches(r, in) {
            result.Requests = append(result.Requests, r)
        }
    }
    return result, err
}

// CloudRequest reads one intent without consulting live cloud credentials.
func (c *Resources) CloudRequest(ctx context.Context, in CloudSelect, id string) (cloud.Receipt, error) {
    if err := ctx.Err(); err != nil {
        return cloud.Receipt{}, err
    }
    if c.Cloud == nil {
        return cloud.Receipt{}, fault.New("CLOUD_FAILED", "cloud_failed", nil)
    }
    r, err := c.Cloud.Receipts.Load(id)
    if err == nil && !receiptMatches(r, in) {
        err = cloudConfigError()
    }
    return r, err
}

// CloudReconcile observes a saved mutation; manual absence requires explicit consent.
func (c *Resources) CloudReconcile(ctx context.Context, in CloudReconcileInput, approve ApproveCloud) (cloud.Receipt, error) {
    saved, err := c.CloudRequest(ctx, in.Select, in.Request)
    if err != nil {
        return saved, err
    }
    if in.NoInstance {
        if err = approveCloud(ctx, in.Yes, approve, CloudApproval{Operation: "no-instance", InstanceID: in.Instance}); err != nil {
            return saved, err
        }
    }
    return c.Cloud.Reconcile(ctx, in.Request, in.Instance, in.NoInstance)
}

// CloudAccounts reads configured account references, not credential material.
func (c *Resources) CloudAccounts() (CloudAccounts, error) {
    cfg, err := c.Config.Load()
    return CloudAccounts{Accounts: cfg.Cloud.Accounts, Default: cfg.Cloud.DefaultAccount}, err
}

// AddCloudAccount saves an explicitly configured provider account.
func (c *Resources) AddCloudAccount(ctx context.Context, in CloudAccountInput) (CloudAccounts, error) {
    if err := c.Config.AddCloudAccount(ctx, in.Name, in.Account, in.Replace, in.Default); err != nil {
        return CloudAccounts{}, err
    }
    return c.CloudAccounts()
}

// RemoveCloudAccount removes configuration without redirecting saved bindings.
func (c *Resources) RemoveCloudAccount(ctx context.Context, name string) (CloudAccounts, error) {
    if err := c.Config.RemoveCloudAccount(ctx, name); err != nil {
        return CloudAccounts{}, err
    }
    return c.CloudAccounts()
}
