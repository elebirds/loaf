package cli

import (
    "context"
    "encoding/json"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
)

type uxCloud struct {
    cloudProvider
    searches   int
    emptyPolls int
    picked     string
}

func (p *uxCloud) Catalog(_ context.Context, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    page := cloud.CatalogPage{Category: in.Category, Items: []cloud.CatalogItem{}, Complete: true}
    switch in.Category {
    case "gpu":
        page.Items = []cloud.CatalogItem{{ID: "RTX 4090", Name: "RTX 4090", Vendor: "nvidia"}}
    case "region":
        page.Items = []cloud.CatalogItem{{ID: "west", Name: "West"}}
    case "image":
        page.Items = []cloud.CatalogItem{{ID: "base-1", Name: "PyTorch / 2.6 / Python 3.12 / CUDA 12.6", Kind: "base", Vendor: "nvidia", Architecture: "x86", CUDA: "12.6"}}
    }
    return page, nil
}
func (p *uxCloud) Search(_ context.Context, _ cloud.Plan, _ int) (cloud.InventoryPage, error) {
    p.searches++
    if p.searches <= p.emptyPolls {
        return cloud.InventoryPage{Offers: []cloud.Offer{}, Complete: true}, nil
    }
    yes := true
    cpu, mem := 16, 64
    return cloud.InventoryPage{Offers: []cloud.Offer{
        {ID: "cheap", Model: "RTX 4090", GPUCount: 1, Vendor: "nvidia", Architecture: "x86", HighestCUDA: "12.8", Available: &yes, CPUs: &cpu, MemoryGiB: &mem, ComputeHourlyPrice: &cloud.Money{Amount: "1.00", Currency: "CNY"}},
        {ID: "wanted", Model: "RTX 4090", GPUCount: 1, Vendor: "nvidia", Architecture: "x86", HighestCUDA: "12.8", Available: &yes, CPUs: &cpu, MemoryGiB: &mem, ComputeHourlyPrice: &cloud.Money{Amount: "2.00", Currency: "CNY"}},
    }, Complete: true, Examined: 2, ObservedAt: time.Now().UTC()}, nil
}
func (p *uxCloud) Create(_ context.Context, _ cloud.Plan, o *cloud.Offer) (cloud.Ref, error) {
    p.creates++
    p.picked = o.ID
    return cloud.Ref{Provider: "autodl", Account: "main", Product: "standard", ID: "only-instance"}, nil
}

func uxFixture(t *testing.T) (testOptions, *uxCloud) {
    t.Helper()
    o := fixture(t)
    p := &uxCloud{}
    o.Cloud = testCloudService(o.Config, func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil })
    o.Operations = &fakeOperations{}
    code, out, err := run(t, o, "cloud", "account", "add", "main", "--token-env", "FIXTURE_TOKEN", "--default", "--json")
    if code != 0 {
        t.Fatal(out, err)
    }
    return o, p
}

func TestCloudUXPreviewIsOfflineAndReportsSourcesAndMissingFields(t *testing.T) {
    o, _ := uxFixture(t)
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        t.Fatal("offline preview opened credentials/provider")
        return nil, nil
    }
    code, out, stderr := run(t, o, "cloud", "acquire", "gpu", "--image", "base-1", "--dry-run", "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, `"builtin"`) || !strings.Contains(out, `"pending_checks"`) || !strings.Contains(out, `"auth_mode":"session"`) {
        t.Fatal(code, out, stderr)
    }
    if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
        t.Fatal("preview wrote state", err)
    }
    code, out, stderr = run(t, o, "cloud", "acquire", "gpu", "--json")
    if code != 2 || stderr != "" || !strings.Contains(out, `"argument":"--image"`) || !strings.Contains(out, `"choose_image"`) || !strings.Contains(out, `"issues"`) {
        t.Fatal(code, out, stderr)
    }
}

func TestCloudUXPinnedAgentAndRequestOnlyRecovery(t *testing.T) {
    o, p := uxFixture(t)
    code, out, stderr := run(t, o, "cloud", "acquire", "gpu", "--offer", "wanted", "--image", "base-1", "--max-compute-hourly-price", "2.50", "--yes", "--json")
    var result struct {
        Context cloudContext
        Data    cloudBinding
    }
    if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &result) != nil || p.picked != "wanted" || p.creates != 1 || result.Context.Mode != "select" || result.Context.RequestID == "" {
        t.Fatal(code, out, stderr, p.picked)
    }
    code, out, stderr = run(t, o, "cloud", "bind", "--request", result.Context.RequestID, "--json")
    if code != 0 || stderr != "" || p.creates != 1 {
        t.Fatal(code, out, stderr)
    }
}

func TestCloudUXContinuousAgentProgressKeepsOneStdoutResult(t *testing.T) {
    o, p := uxFixture(t)
    p.emptyPolls = 3
    o.Cloud.Wait = func(context.Context, time.Duration) error { return nil }
    code, out, stderr := run(t, o, "cloud", "acquire", "gpu", "--image", "base-1", "--gpu-model", "RTX 4090", "--max-compute-hourly-price", "2.50", "--continuous", "--progress=json", "--yes", "--json")
    if code != 0 || len(strings.Split(strings.TrimSpace(out), "\n")) != 1 || p.creates != 1 || strings.Contains(out+stderr, "\x1b") {
        t.Fatal(code, out, stderr)
    }
    events := strings.Split(strings.TrimSpace(stderr), "\n")
    if len(events) < 5 {
        t.Fatal(stderr)
    }
    for _, line := range events {
        var e struct{ Type, Stage, Operation string }
        if json.Unmarshal([]byte(line), &e) != nil || e.Type != "progress" || e.Stage == "" || e.Operation != "acquire" {
            t.Fatal(line, e)
        }
    }
}

func TestCloudUXHumanWizardSelectsActualHostAndNeverConfirmsImplicitly(t *testing.T) {
    for _, accept := range []bool{false, true} {
        t.Run(map[bool]string{false: "cancel", true: "accept"}[accept], func(t *testing.T) {
            o, p := uxFixture(t)
            o.Interactive = true
            answers := []string{"", "1", "", "", "", "", "2", "", "", "", "", "", "", "", "", "no"}
            if accept {
                answers[len(answers)-1] = "yes"
                answers[13] = "1,2"
            }
            o.In = strings.NewReader(strings.Join(answers, "\n") + "\n")
            code, out, stderr := run(t, o, "-i", "cloud", "acquire")
            if accept {
                if code != 0 || p.creates != 1 || p.picked != "wanted" {
                    t.Fatal(code, out, stderr, p.creates, p.picked)
                }
                settings, err := o.Config.Load()
                if err != nil || settings.Resources["gpu"].AutoDL.Image == nil || *settings.Resources["gpu"].AutoDL.Image != "base-1" {
                    t.Fatal(settings, err)
                }
            } else if code == 0 || p.creates != 0 {
                t.Fatal(code, out, stderr, p.creates)
            }
        })
    }
}

func TestCloudUXOptionalProfileReplacementNeedsConfirmation(t *testing.T) {
    for _, answer := range []string{"", "yes"} {
        t.Run(map[string]string{"": "keep", "yes": "replace"}[answer], func(t *testing.T) {
            o, p := uxFixture(t)
            original := "original-image"
            if err := o.Config.SaveCloudProfile(context.Background(), "gpu", cloud.Profile{AutoDL: cloud.AutoDLProfile{Image: &original}}, false); err != nil {
                t.Fatal(err)
            }
            o.Interactive = true
            answers := []string{"", "1", "", "", "", "", "2", "", "", "", "", "", "", "2", "", "yes", answer}
            o.In = strings.NewReader(strings.Join(answers, "\n") + "\n")
            code, out, diagnostic := run(t, o, "-i", "cloud", "acquire")
            if code != 0 || p.creates != 1 {
                t.Fatal(code, out, diagnostic)
            }
            settings, err := o.Config.Load()
            want := original
            if answer == "yes" {
                want = "base-1"
            }
            if err != nil || *settings.Resources["gpu"].AutoDL.Image != want {
                t.Fatal(settings, err)
            }
        })
    }
}

type wizardCloner struct{ *cliCloner }

func (p *wizardCloner) Catalog(ctx context.Context, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    return (&uxCloud{}).Catalog(ctx, in)
}
func (p *wizardCloner) Search(context.Context, cloud.Plan, int) (cloud.InventoryPage, error) {
    yes := true
    return cloud.InventoryPage{Complete: true, Examined: 1, Offers: []cloud.Offer{{ID: "host", Model: "RTX 4090", GPUCount: 1, Vendor: "nvidia", Region: "west-D", Available: &yes, ComputeHourlyPrice: &cloud.Money{Amount: "2.78", Currency: "CNY"}, StorageDailyPrice: &cloud.Money{Amount: "1.00", Currency: "CNY"}}}}, nil
}

func TestCloudUXCloneWizardKeepsRemovedAccountsFrozenSource(t *testing.T) {
    o, base := cloneCLIFixture(t)
    p := &wizardCloner{base}
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    binding := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "ordinary", ID: "old-source"}, Settings: settings.Cloud.Accounts["ordinary"]}
    if err := o.Config.Add(context.Background(), "source", config.Target{Cloud: &binding, RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    if err := o.Config.RemoveCloudAccount(context.Background(), "ordinary"); err != nil {
        t.Fatal(err)
    }
    o.Cloud.Open = func(_ context.Context, name string, account cloud.Account, product string) (cloud.Provider, error) {
        if name != "ordinary" || account != binding.Settings || product != "standard" {
            t.Fatal("source principal changed", name, account, product)
        }
        return p, nil
    }
    o.Interactive = true
    o.In = strings.NewReader(strings.Join([]string{"1", "1", "", "", "", "", "", "", "", "", "", "", "", "yes"}, "\n") + "\n")
    code, out, diagnostic := run(t, o, "-i", "cloud", "acquire", "--clone-from", "source")
    if code != 0 || p.clones != 1 || p.creates != 0 || p.plan.Clone == nil || p.plan.Clone.Instance != binding.Ref || p.plan.AutoDL.ExpandDataDiskGB != 150 {
        t.Fatal(code, out, diagnostic, p)
    }
}

func TestCloudUXExplicitConflictsAndHelpRelationships(t *testing.T) {
    o, p := uxFixture(t)
    for _, args := range [][]string{
        {"--continuous", "--wait", "1m", "--image", "base-1", "--max-compute-hourly-price", "2.00"},
        {"--offer", "wanted", "--continuous", "--image", "base-1", "--max-compute-hourly-price", "2.00"},
        {"--continuous", "--image", "base-1"},
    } {
        code, out, stderr := run(t, o, append(append([]string{"cloud", "acquire", "gpu"}, args...), "--yes", "--json")...)
        if code != 2 || p.creates != 0 || stderr != "" {
            t.Fatal(code, out, stderr)
        }
    }
    _, out, _ := run(t, o, "help", "cloud", "acquire", "--json")
    if !strings.Contains(out, `"conflicts_with"`) || !strings.Contains(out, `"exactly_one_of"`) {
        t.Fatal(out)
    }
}
