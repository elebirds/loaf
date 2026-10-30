package cli

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
)

type acquisitionPlanCloud struct {
    *uxCloud
    acquired cloud.Plan
}

func (p *acquisitionPlanCloud) Create(ctx context.Context, plan cloud.Plan, offer *cloud.Offer) (cloud.Ref, error) {
    p.acquired = cloneCloudPlan(plan)
    return p.uxCloud.Create(ctx, plan, offer)
}

func TestCloudAcquireCompleteInputsReviewWithoutRefillingOrChangingDefaults(t *testing.T) {
    o, base := uxFixture(t)
    provider := &acquisitionPlanCloud{uxCloud: base}
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return provider, nil }
    if code, out, diagnostic := run(t, o, "machine", "add", "original", "--ssh", "old-machine", "--json"); code != 0 {
        t.Fatal(code, out, diagnostic)
    }
    if code, out, diagnostic := run(t, o, "machine", "use", "original", "--json"); code != 0 {
        t.Fatal(code, out, diagnostic)
    }
    o.Interactive = true
    o.In = strings.NewReader("\n\nyes\n")
    code, out, diagnostic := run(t, o, "cloud", "acquire", "new-gpu", "-i", "--gpu-model=", "--region=", "--min-cpus=0", "--expand-data-disk=0", "--wait=0s", "--image=base-1", "--max-compute-hourly-price=3", "--progress=none")
    if code != 0 || provider.creates != 1 || strings.Contains(diagnostic, "How would you like") || strings.Contains(diagnostic, "Optional actions") {
        t.Fatal(code, out, diagnostic, provider.creates)
    }
    plan := provider.acquired
    if len(plan.Requirements.GPUModels) != 0 || len(plan.Requirements.Regions) != 0 || plan.Requirements.MinCPUs != 0 || plan.AutoDL.ExpandDataDiskGB != 0 || plan.WaitTimeout != "0s" {
        t.Fatal("review lost explicit empty/zero overrides", plan)
    }
    settings, err := o.Config.Load()
    if err != nil || len(settings.Resources) != 0 || settings.Targets["new-gpu"].Cloud == nil {
        t.Fatal(settings, err)
    }
    root, _, err := config.Discover(context.Background(), o.CWD)
    if err != nil {
        t.Fatal(err)
    }
    project, err := o.State.Existing(root)
    if err != nil || project.Target != "original" {
        t.Fatal("acquisition changed the project's selected machine", project, err)
    }
    if !strings.Contains(out, "loaf machine use new-gpu") {
        t.Fatal("completion omitted the explicit new-machine selection", out)
    }
}

func TestCloudAcquireNoAccountLogsInWithinTheSameNativeSession(t *testing.T) {
    o, logins := authFixture(t)
    for _, name := range []string{"demo", "ordinary"} {
        if err := o.Config.RemoveCloudAccount(context.Background(), name); err != nil {
            t.Fatal(err)
        }
    }
    provider := &uxCloud{}
    o.Cloud.Open = func(_ context.Context, name string, account cloud.Account, product string) (cloud.Provider, error) {
        if name != "main" || account.Credential.Identity.ID != "verified-sub" || product != "standard" {
            t.Fatal("acquisition did not consume the newly saved account", name, account, product)
        }
        return provider, nil
    }
    o.Interactive = true
    o.ReadPassword = func(context.Context) ([]byte, error) { return []byte(" raw password "), nil }
    o.In = strings.NewReader("main\nstudent@master\n\n\nyes\n")
    code, out, diagnostic := run(t, o, "cloud", "acquire", "gpu", "-i", "--image=base-1", "--max-compute-hourly-price=3", "--progress=none")
    if code != 0 || *logins != 1 || provider.creates != 1 || strings.Contains(diagnostic, "Accounts and profiles") {
        t.Fatal(code, out, diagnostic, *logins, provider.creates)
    }
    for _, secret := range []string{"raw password", "login-token-private", "device-private"} {
        if strings.Contains(out+diagnostic, secret) {
            t.Fatal("native login secret leaked", secret)
        }
    }
}

func TestCloudAcquireBindingFailureEndsWithOriginalRequest(t *testing.T) {
    o, provider := uxFixture(t)
    o.Interactive = true
    o.In = strings.NewReader("\n\nyes\n")
    o.Operations = &fakeOperations{err: errors.New("fixture connection refused")}
    code, out, diagnostic := run(t, o, "cloud", "acquire", "gpu", "-i", "--image=base-1", "--max-compute-hourly-price=3", "--ready-timeout=0s", "--progress=none")
    if code != 1 || provider.creates != 1 || !strings.Contains(out, "loaf cloud bind --request ") || strings.Contains(diagnostic, "Try connecting again") {
        t.Fatal(code, out, diagnostic, provider.creates)
    }
}

type uncertainAcquisitionCloud struct{ *uxCloud }

func (p *uncertainAcquisitionCloud) Create(context.Context, cloud.Plan, *cloud.Offer) (cloud.Ref, error) {
    p.creates++
    return cloud.Ref{}, &cloud.APIError{Code: "TRANSPORT", Unconfirmed: true}
}

func TestCloudAcquireUnconfirmedEndsWithoutAnotherPurchase(t *testing.T) {
    o, base := uxFixture(t)
    provider := &uncertainAcquisitionCloud{uxCloud: base}
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return provider, nil }
    o.Interactive = true
    o.In = strings.NewReader("\n\nyes\n")
    code, out, diagnostic := run(t, o, "cloud", "acquire", "gpu", "-i", "--image=base-1", "--max-compute-hourly-price=3", "--progress=none")
    if code != 3 || provider.creates != 1 || !strings.Contains(out, "loaf cloud requests c_") || strings.Contains(diagnostic, "Try connecting again") {
        t.Fatal(code, out, diagnostic, provider.creates)
    }
}

func TestCloudAcquireCompleteNonterminalInputsStayDirectWithInteractiveFlag(t *testing.T) {
    for _, flags := range [][]string{{"--yes"}, {"--dry-run"}} {
        o, provider := uxFixture(t)
        o.In = strings.NewReader("")
        args := append([]string{"cloud", "acquire", "gpu", "-i", "--image=base-1", "--max-compute-hourly-price=3", "--progress=none"}, flags...)
        code, out, diagnostic := run(t, o, args...)
        wanted := 1
        if flags[0] == "--dry-run" {
            wanted = 0
        }
        if code != 0 || provider.creates != wanted || strings.Contains(diagnostic, "> ") {
            t.Fatal(flags, code, out, diagnostic, provider.creates)
        }
    }
}

func TestCloudLifecycleMissingIdentityNeverOpensManagementMenus(t *testing.T) {
    for _, command := range []string{"info", "start", "stop", "bind"} {
        o, provider := uxFixture(t)
        o.Interactive = true
        o.In = strings.NewReader("")
        code, out, diagnostic := run(t, o, "cloud", command, "-i")
        if code != 2 || provider.gets != 0 || provider.creates != 0 || strings.Contains(diagnostic, "> ") {
            t.Fatal(command, code, out, diagnostic, provider)
        }
    }
}
