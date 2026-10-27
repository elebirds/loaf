package cli

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
)

type cliCloner struct {
    *cloudProvider
    source                    cloud.CloneSource
    sources, clones, searches int
    status, region            string
    plan                      cloud.Plan
    cloneError                error
}

func (p *cliCloner) CloneSource(_ context.Context, r cloud.Ref) (cloud.CloneSource, error) {
    p.sources++
    s := p.source
    s.Instance = r
    return s, nil
}
func (p *cliCloner) Clone(_ context.Context, plan cloud.Plan, _ *cloud.Offer) (cloud.Ref, error) {
    p.clones++
    p.plan = plan
    return cloud.Ref{Provider: "autodl", Product: "standard", Account: plan.Clone.Instance.Account, ID: "new-clone"}, p.cloneError
}
func (p *cliCloner) Search(_ context.Context, plan cloud.Plan, _ int) (cloud.InventoryPage, error) {
    p.searches++
    yes := true
    return cloud.InventoryPage{Complete: true, Offers: []cloud.Offer{{ID: "machine", GPUCount: 1, Vendor: "nvidia", Region: "west-D", Available: &yes, ComputeHourlyPrice: &cloud.Money{Amount: "2.78", Currency: "CNY"}}}}, nil
}
func (p *cliCloner) Get(_ context.Context, r cloud.Ref) (cloud.Instance, error) {
    return cloud.Instance{Ref: r, Status: "running", Region: p.region, CloneDataStatus: p.status}, nil
}

func cloneCLIFixture(t *testing.T) (testOptions, *cliCloner) {
    t.Helper()
    o, p := cloudFixture(t)
    c := &cliCloner{cloudProvider: p, status: "succeeded", region: "west-D", source: cloud.CloneSource{Region: "west-D", Image: "original-image", Vendor: "nvidia", Architecture: "x86", CUDA: "12.8", ExpandDataDiskGB: 150}}
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return c, nil }
    return o, c
}

func cloneArgs() []string {
    return []string{"cloud", "acquire", "gpu", "--account", "ordinary", "--clone-from", "old-source", "--max-compute-hourly-price", "3.00", "--ready-timeout", "0s", "--yes", "--json"}
}

func TestCloudCloneAcquiresInheritsAndReturnsOneJSONResult(t *testing.T) {
    o, p := cloneCLIFixture(t)
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[resources.default.autodl]\nimage = 'unrelated-configured-image'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    code, out, diagnostic := run(t, o, cloneArgs()...)
    var e struct {
        OK   bool
        Data cloudBinding
    }
    if code != 0 || diagnostic != "" || strings.Count(strings.TrimSpace(out), "\n") != 0 || json.Unmarshal([]byte(out), &e) != nil || !e.OK || e.Data.Status != "ready" || e.Data.CloneDataStatus != "succeeded" {
        t.Fatal(code, out, diagnostic)
    }
    if p.clones != 1 || p.creates != 0 || p.sources != 1 || p.searches != 2 || p.plan.Clone == nil || p.plan.Clone.Instance.ID != "old-source" || p.plan.AutoDL.Image != "" || p.plan.AutoDL.ExpandDataDiskGB != 150 || p.plan.WaitTimeout != "30m" || e.Data.Request == nil {
        t.Fatal(p, e)
    }
    saved, err := o.Cloud.Receipts.Load(e.Data.Request.ID)
    if err != nil || saved.Plan.Clone.Instance.ID != "old-source" || saved.Status != "ready" {
        t.Fatal(saved, err)
    }
}

func TestCloudCloneWaitsForDataAndRecoversWithoutSecondAllocation(t *testing.T) {
    for _, status := range []string{"copying", "unknown", "", "failed"} {
        t.Run(status, func(t *testing.T) {
            o, p := cloneCLIFixture(t)
            p.status = status
            code, out, diagnostic := run(t, o, cloneArgs()...)
            want := "CLOUD_NOT_READY"
            if status == "failed" {
                want = "CLONE_FAILED"
            }
            if code != 1 || diagnostic != "" || !strings.Contains(out, want) || strings.Contains(out, `"status":"ready"`) || p.clones != 1 {
                t.Fatal(code, out, diagnostic)
            }
            var e struct{ Data cloudBinding }
            if json.Unmarshal([]byte(out), &e) != nil || e.Data.Request == nil {
                t.Fatal(out)
            }
            p.status = "succeeded"
            code, out, diagnostic = run(t, o, "cloud", "bind", "gpu", "--request", e.Data.Request.ID, "--ready-timeout", "0s", "--json")
            if code != 0 || diagnostic != "" || !strings.Contains(out, `"status":"ready"`) || p.clones != 1 || p.creates != 0 {
                t.Fatal(code, out, diagnostic)
            }
        })
    }
}

func TestCloudCloneRejectsUnsafeOptionsAndUnsupportedProducts(t *testing.T) {
    for _, extra := range [][]string{{"--region", "west-X"}, {"--region", "west-D,west-X"}, {"--expand-data-disk", "0"}, {"--image", "other"}, {"--private-image", "other"}, {"--gpu-spec", "spec"}, {"--cuda-from", "128"}, {"--expand-system-disk", "0"}, {"--allow-unquoted"}, {"--clone-from", ""}} {
        o, p := cloneCLIFixture(t)
        code, out, _ := run(t, o, append(cloneArgs(), extra...)...)
        if code != 2 || p.clones != 0 || p.creates != 0 {
            t.Fatal(extra, code, out, p)
        }
        if rows, err := o.Cloud.Receipts.List(); err != nil || len(rows) != 0 {
            t.Fatal("invalid request wrote intent", rows, err)
        }
    }
    o, _ := cloneCLIFixture(t)
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        t.Fatal("unsupported clone accessed credentials/provider")
        return nil, nil
    }
    code, out, _ := run(t, o, "cloud", "acquire", "gpu", "--clone-from", "old-source", "--yes", "--json")
    if code != 1 || !strings.Contains(out, "UNSUPPORTED_CAPABILITY") {
        t.Fatal(code, out)
    }
}

func TestCloudClonePreservesAliasAccountAndExplicitWait(t *testing.T) {
    o, p := cloneCLIFixture(t)
    cfg, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    original := cfg.Cloud.Accounts["ordinary"]
    b := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "ordinary", ID: "bound-source"}, Settings: original}
    if err := o.Config.Add(context.Background(), "source-alias", config.Target{RemoteBase: "~/projects", Cloud: &b}, false); err != nil {
        t.Fatal(err)
    }
    changed := original
    changed.Credential.Env = "NEW_TOKEN"
    if err := o.Config.AddCloudAccount(context.Background(), "ordinary", changed, true, false); err != nil {
        t.Fatal(err)
    }
    o.Cloud.Open = func(_ context.Context, name string, a cloud.Account, product string) (cloud.Provider, error) {
        if name != "ordinary" || a != original || product != "standard" {
            t.Fatal("source alias reinterpreted", name, a, product)
        }
        return p, nil
    }
    code, out, _ := run(t, o, "cloud", "acquire", "gpu", "--clone-from", "source-alias", "--wait", "0s", "--yes", "--ready-timeout", "0s", "--json")
    if code != 0 || p.plan.Clone.Instance.ID != "bound-source" || p.plan.WaitTimeout != "0s" {
        t.Fatal(code, out, p.plan)
    }
}

func TestCloudCloneConfirmationHelpAndFlagParsing(t *testing.T) {
    o, p := cloneCLIFixture(t)
    args := cloneArgs()
    args = append(args[:len(args)-2], "--json")
    code, out, diagnostic := run(t, o, args...)
    if code != 2 || diagnostic != "" || p.clones != 0 || !strings.Contains(out, "BAD_CONFIG") {
        t.Fatal(code, out, diagnostic)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, diagnostic = run(t, o, "--lang", lang, "cloud", "acquire", "--help")
        if code != 0 || diagnostic != "" || !strings.Contains(out, "--clone-from") || !strings.Contains(out, "30") {
            t.Fatal(code, out, diagnostic)
        }
    }
    code, out, _ = run(t, o, "cloud", "acquire", "gpu", "--clone-from", "--json")
    if code != 2 || strings.HasPrefix(out, "{") {
        t.Fatal("flag value changed JSON mode", code, out)
    }
}

func TestCloudCloneRawSourceUsesProfileProductBeforeAccountDefault(t *testing.T) {
    o, p := cloneCLIFixture(t)
    cfg, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    account := cfg.Cloud.Accounts["ordinary"]
    account.DefaultProduct = "pro"
    if err := o.Config.AddCloudAccount(context.Background(), "ordinary", account, true, false); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[resources.default]\nproduct = 'standard'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    o.Cloud.Open = func(_ context.Context, _ string, _ cloud.Account, product string) (cloud.Provider, error) {
        if product != "standard" {
            t.Fatal(product)
        }
        return p, nil
    }
    code, out, _ := run(t, o, cloneArgs()...)
    if code != 0 || p.plan.Product != "standard" || p.plan.Clone.Instance.Product != "standard" {
        t.Fatal(code, out, p.plan)
    }
}
