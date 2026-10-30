package cli

import (
    "context"
    "encoding/json"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
)

func TestCloudHubHelpNeverOpensProvider(t *testing.T) {
    for _, args := range [][]string{{"cloud"}, {"cloud", "--json"}, {"cloud", "--help"}, {"help", "cloud"}, {"cloud", "--help", "--json"}} {
        o, _ := uxFixture(t)
        o.Interactive = len(args) > 1
        o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
            t.Fatal("help opened provider", args)
            return nil, nil
        }
        code, out, stderr := run(t, o, args...)
        if code != 0 || stderr != "" || out == "" {
            t.Fatal(args, code, out, stderr)
        }
    }
}

func TestCloudHubSessionScopeDoesNotChangeDefault(t *testing.T) {
    o, p := uxFixture(t)
    if code, out, err := run(t, o, "cloud", "account", "add", "other", "--token-env", "OTHER_TOKEN", "--json"); code != 0 {
        t.Fatal(out, err)
    }
    seen := []string{}
    o.Cloud.Open = func(_ context.Context, name string, _ cloud.Account, product string) (cloud.Provider, error) {
        seen = append(seen, name+"/"+product)
        return p, nil
    }
    o.Interactive = true
    // Settings / scope / other / Pro / back / exit.
    o.In = strings.NewReader("4\n1\n2\n2\n7\n5\n")
    code, out, err := run(t, o, "-i", "cloud")
    if code != 0 || strings.Contains(out+err, "OTHER_TOKEN") {
        t.Fatal(code, out, err)
    }
    settings, e := o.Config.Load()
    if e != nil || settings.Cloud.DefaultAccount != "main" {
        t.Fatal(settings, e)
    }
    if !reflect.DeepEqual(seen, []string{"main/standard", "other/pro"}) {
        t.Fatal("unexpected scope traversal", seen)
    }
}

type lifecycleCloud struct{ *uxCloud }

func (p *lifecycleCloud) List(context.Context, int) (cloud.Page, error) {
    return cloud.Page{Complete: false, Examined: 1, Instances: []cloud.Instance{{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "main", ID: "only-instance"}, Name: "machine", Status: "running", Region: "west"}}}, nil
}
func TestCloudHubExistingCredentialsRemainRedacted(t *testing.T) {
    o, base := uxFixture(t)
    p := &lifecycleCloud{base}
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil }
    o.Interactive = true
    o.In = strings.NewReader("1\n1\n3\n1\n8\n4\n5\n")
    code, out, err := run(t, o, "-i", "cloud")
    if code != 0 || strings.Contains(out+err, "fixture-ssh-secret") || p.creates != 0 || !strings.Contains(err, "only-instance") {
        t.Fatal(code, out, err, p.creates)
    }
}

func TestCloudProfilesOfflineSaveRemoveAndExplicitValues(t *testing.T) {
    o, _ := uxFixture(t)
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        t.Fatal("offline profile accessed provider")
        return nil, nil
    }
    args := []string{"cloud", "profile", "save", "empty", "--gpu-model=", "--region=", "--min-cpus=0", "--expand-data-disk=0", "--continuous=false", "--wait=0s", "--json"}
    code, out, stderr := run(t, o, args...)
    if code != 0 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
    settings, e := o.Config.Load()
    p := settings.Resources["empty"]
    if e != nil || p.GPUModels == nil || len(p.GPUModels) != 0 || p.Regions == nil || *p.MinCPUs != 0 || p.Continuous == nil || *p.Continuous || *p.AutoDL.ExpandDataDiskGB != 0 {
        t.Fatal(p, e)
    }
    code, out, _ = run(t, o, "cloud", "profile", "remove", "empty", "--json")
    if code != 2 {
        t.Fatal(code, out)
    }
    code, out, stderr = run(t, o, "cloud", "profile", "remove", "empty", "--yes", "--json")
    if code != 0 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
    settings, _ = o.Config.Load()
    if _, exists := settings.Resources["empty"]; exists {
        t.Fatal("profile not removed")
    }
}

func TestCloudEquivalentArgvRoundTrips(t *testing.T) {
    for _, extra := range [][]string{
        {"--continuous=false", "--wait=0s", "--region=", "--gpu-model=", "--min-cpus=0", "--expand-data-disk=0"},
        {"--continuous", "--gpu-model=RTX 4090", "--region=west", "--poll-interval=10s"},
        {"--offer=" + encodeOfferSelection("wanted", 150)},
    } {
        o, _ := uxFixture(t)
        o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
            t.Fatal("preview accessed provider")
            return nil, nil
        }
        args := append([]string{"cloud", "acquire", "gpu", "--image=base-1", "--max-compute-hourly-price=3", "--dry-run", "--json"}, extra...)
        code, out, stderr := run(t, o, args...)
        var first struct {
            Data        client.CloudPreview
            NextActions []cloudNextAction `json:"next_actions"`
        }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &first) != nil || len(first.NextActions) != 1 {
            t.Fatal(code, out, stderr)
        }
        argv := first.NextActions[0].Command
        for _, arg := range argv {
            if arg == "--yes" || strings.Contains(arg, "TOKEN") {
                t.Fatal("unsafe command", argv)
            }
        }
        code, out, stderr = run(t, o, append(argv[1:], "--dry-run", "--json")...)
        var second struct{ Data client.CloudPreview }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &second) != nil || !reflect.DeepEqual(first.Data.Plan, second.Data.Plan) {
            t.Fatalf("%v\n%d %s %s\n%+v\n%+v", argv, code, out, stderr, first.Data.Plan, second.Data.Plan)
        }
    }
}

func TestCloudCompleteProfileStartsAtReviewAndCanSaveBeforeRent(t *testing.T) {
    o, p := uxFixture(t)
    code, out, stderr := run(t, o, "cloud", "profile", "save", "train", "--gpu-model=RTX 4090", "--image=base-1", "--max-compute-hourly-price=3", "--continuous", "--json")
    if code != 0 {
        t.Fatal(out, stderr)
    }
    o.Interactive = true
    // Review: alias, optional actions, save-only, no payment; profile name.
    o.In = strings.NewReader("\n\n5\n\nsaved\n")
    code, out, stderr = run(t, o, "-i", "cloud", "acquire", "--profile=train")
    if code != 0 || p.creates != 0 || strings.Contains(stderr, "How would you like") {
        t.Fatal(code, out, stderr, p.creates)
    }
    settings, _ := o.Config.Load()
    if settings.Resources["saved"].AutoDL.Image == nil {
        t.Fatal("profile not saved")
    }
}

func TestCloudEditNeverRestartsCreatedOrUncertainRequest(t *testing.T) {
    for _, status := range []string{"intent", "uncertain", "created", "not_created", "no_stock", "rejected"} {
        result := client.AcquisitionResult{Acquisition: &client.CloudAcquisition{Receipt: cloud.Receipt{Status: status}}}
        want := status == "not_created" || status == "no_stock" || status == "rejected"
        if canEditCloudAcquisition(result) != want {
            t.Fatal(status)
        }
        result.Acquisition.Receipt.Instance = &cloud.Ref{ID: "original"}
        if canEditCloudAcquisition(result) {
            t.Fatal("created instance was replaceable")
        }
    }
}

func TestCloudCandidateSortUnknownAndStableTies(t *testing.T) {
    rows := []cloud.Offer{{ID: "unknown"}, {ID: "b", CPUs: cloudPointer(8), ComputeHourlyPrice: &cloud.Money{Amount: "1", Currency: "CNY"}}, {ID: "a", CPUs: cloudPointer(16), ComputeHourlyPrice: &cloud.Money{Amount: "1.00", Currency: "CNY"}}}
    for _, order := range []string{"price", "cpu"} {
        sortCandidates(rows, order)
        if rows[0].ID != "a" || rows[2].ID != "unknown" {
            t.Fatal(order, rows)
        }
    }
}

func TestCloudReadinessDefaultsAndZero(t *testing.T) {
    model := &grammar{}
    p, _ := newParser(model)
    if _, err := p.Parse([]string{"cloud", "acquire"}); err != nil || model.Cloud.Acquire.ReadyTimeout != 5*time.Minute {
        t.Fatal(err, model.Cloud.Acquire.ReadyTimeout)
    }
    model = &grammar{}
    p, _ = newParser(model)
    _, _ = p.Parse([]string{"cloud", "bind", "--ready-timeout=0s"})
    if model.Cloud.Bind.ReadyTimeout != 0 {
        t.Fatal(model.Cloud.Bind.ReadyTimeout)
    }
}

func TestCloudCloneEquivalentCommandPreservesPendingInheritance(t *testing.T) {
    o, _ := cloneCLIFixture(t)
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        t.Fatal("clone preview opened provider")
        return nil, nil
    }
    args := []string{"cloud", "acquire", "copy", "--clone-from=source-id", "--account=ordinary", "--max-compute-hourly-price=3", "--wait=0s", "--expand-data-disk=0", "--region=", "--dry-run", "--json"}
    code, out, err := run(t, o, args...)
    var first struct {
        Data        client.CloudPreview
        NextActions []cloudNextAction `json:"next_actions"`
    }
    if code != 0 || err != "" || json.Unmarshal([]byte(out), &first) != nil || len(first.NextActions) != 1 {
        t.Fatal(code, out, err)
    }
    argv := first.NextActions[0].Command
    for _, arg := range argv {
        for _, forbidden := range []string{"--yes", "--image=", "--private-image=", "--cuda-from=", "--expand-system-disk="} {
            if strings.HasPrefix(arg, forbidden) {
                t.Fatal(argv)
            }
        }
    }
    code, out, err = run(t, o, append(argv[1:], "--dry-run", "--json")...)
    var second struct{ Data client.CloudPreview }
    if code != 0 || err != "" || json.Unmarshal([]byte(out), &second) != nil || !reflect.DeepEqual(first.Data.Plan, second.Data.Plan) || !reflect.DeepEqual(first.Data.Inherited, second.Data.Inherited) {
        t.Fatal(argv, code, out, err)
    }
}

func TestCloudLocalSemanticIssuesAreCollectedTogether(t *testing.T) {
    o, _ := cloudFixture(t)
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        t.Fatal("invalid local input accessed provider")
        return nil, nil
    }
    code, out, stderr := run(t, o, "cloud", "acquire", "gpu", "--continuous", "--json")
    if code != 2 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
    for _, field := range []string{"--max-compute-hourly-price", "--image", "--cuda-from", "--gpu-spec"} {
        if !strings.Contains(out, `"argument":"`+field+`"`) {
            t.Fatal(field, out)
        }
    }
    code, out, stderr = run(t, o, "cloud", "acquire", "gpu", "--poll-interval=1s", "--image=present", "--json")
    if code != 2 || strings.Contains(out, `"argument":"--image"`) {
        t.Fatal("unrelated issue invented", code, out, stderr)
    }
}

func TestCloudEquivalentCommandClearsConflictingInheritedEnvironment(t *testing.T) {
    o, _ := uxFixture(t)
    defaults := cloud.Profile{AutoDL: cloud.AutoDLProfile{Image: cloudPointer("old-image"), GPUSpecUUID: cloudPointer("old-spec"), GPUSpecID: cloudPointer(8), CUDAFrom: cloudPointer(128)}}
    if err := o.Config.SaveCloudProfile(context.Background(), "default", defaults, false); err != nil {
        t.Fatal(err)
    }
    args := []string{"cloud", "acquire", "gpu", "--private-image=private", "--image=", "--gpu-spec=", "--gpu-spec-id=0", "--cuda-from=0", "--dry-run", "--json"}
    code, out, err := run(t, o, args...)
    var first struct {
        Data        client.CloudPreview
        NextActions []cloudNextAction `json:"next_actions"`
    }
    if code != 0 || err != "" || json.Unmarshal([]byte(out), &first) != nil || len(first.NextActions) != 1 {
        t.Fatal(code, out, err)
    }
    code, out, err = run(t, o, append(first.NextActions[0].Command[1:], "--dry-run", "--json")...)
    var second struct{ Data client.CloudPreview }
    if code != 0 || err != "" || json.Unmarshal([]byte(out), &second) != nil || !reflect.DeepEqual(first.Data.Plan, second.Data.Plan) {
        t.Fatal(code, out, err, first.NextActions)
    }
}

func TestCloudCompleteAnyGPUProfileKeepsExplicitEmptyModelList(t *testing.T) {
    o, p := uxFixture(t)
    code, out, stderr := run(t, o, "cloud", "profile", "save", "any-gpu", "--gpu-model=", "--image=base-1", "--max-compute-hourly-price=3", "--json")
    if code != 0 {
        t.Fatal(code, out, stderr)
    }
    o.Interactive = true
    o.In = strings.NewReader("\n\n5\n\nany-copy\n")
    code, out, stderr = run(t, o, "-i", "cloud", "acquire", "--profile=any-gpu")
    if code != 0 || p.creates != 0 || strings.Contains(stderr, "How would you like") {
        t.Fatal(code, out, stderr)
    }
    settings, _ := o.Config.Load()
    if v := settings.Resources["any-copy"].GPUModels; v == nil || len(v) != 0 {
        t.Fatal(v)
    }
}

func TestCloudDraftResetsOnlyIncompatibleInheritedProductFields(t *testing.T) {
    d := cloudDraft{plan: cloud.Plan{Product: "standard", AutoDL: cloud.ImageOptions{CUDAFrom: 128, GPUSpecID: 9, GPUSpecUUID: "pro-spec", ExpandSystemDiskGB: 50}}}
    d.normalizeProductEnvironment("session")
    if !d.environmentReset || d.plan.AutoDL.CUDAFrom != 0 || d.plan.AutoDL.GPUSpecID != 0 || d.plan.AutoDL.GPUSpecUUID != "" || d.plan.AutoDL.ExpandSystemDiskGB != 0 || d.in.Plan.Explicit.AutoDL.CUDAFrom == nil {
        t.Fatal(d)
    }
    d = cloudDraft{plan: cloud.Plan{Product: "standard", AutoDL: cloud.ImageOptions{CUDAFrom: 128}}, in: client.CloudAcquireInput{Plan: client.CloudPlanInput{Explicit: cloud.Profile{AutoDL: cloud.AutoDLProfile{CUDAFrom: cloudPointer(128)}}}}}
    d.normalizeProductEnvironment("session")
    if d.plan.AutoDL.CUDAFrom != 128 || d.environmentReset {
        t.Fatal("explicit conflicting input was silently changed")
    }
}

func TestCloudHubUsesItsScopeInsteadOfAProfileProduct(t *testing.T) {
    o, p := uxFixture(t)
    if err := o.Config.SaveCloudProfile(context.Background(), "default", cloud.Profile{Product: cloudPointer("pro")}, false); err != nil {
        t.Fatal(err)
    }
    products := []string{}
    o.Cloud.Open = func(_ context.Context, _ string, _ cloud.Account, product string) (cloud.Provider, error) {
        products = append(products, product)
        return p, nil
    }
    o.Interactive = true
    // Root / market / GPU catalog / back / back / exit.
    o.In = strings.NewReader("2\n2\n2\n7\n5\n")
    code, out, err := run(t, o, "-i", "cloud")
    if code != 0 {
        t.Fatal(code, out, err)
    }
    for _, product := range products {
        if product != "standard" {
            t.Fatal("profile redirected hub scope", products)
        }
    }
}
