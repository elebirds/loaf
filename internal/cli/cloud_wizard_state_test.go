package cli

import (
    "context"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/i18n"
)

func TestCloudWizardStageTransitions(t *testing.T) {
    for _, tt := range []struct {
        from       cloudStage
        transition cloudTransition
        to         cloudStage
        approved   bool
    }{
        {cloudCompute, cloudNext, cloudCandidate, false},
        {cloudCompute, cloudBack, cloudCompute, false},
        {cloudReview, cloudBack, cloudEnvironment, false},
        {cloudReview, cloudStay, cloudReview, false},
        {cloudReview, cloudRetryCandidate, cloudCandidate, false},
        {cloudReview, cloudRestart, cloudCompute, false},
        {cloudReview, cloudNext, cloudDone, true},
        {cloudDone, cloudRestart, cloudCompute, false},
    } {
        d := cloudDraft{stage: tt.from, approved: &cloudApproved{}}
        d.move(tt.transition)
        if d.stage != tt.to || (d.approved != nil) != tt.approved {
            t.Fatal(tt, d.stage, d.approved)
        }
    }
}

func TestCloudApprovalSnapshotIsIndependentAndMatchesExactIdentity(t *testing.T) {
    d := cloudDraft{
        in: client.CloudAcquireInput{Name: "gpu", Select: client.CloudSelect{Account: cloudPointer("account")}},
        plan: cloud.Plan{Product: "standard", Requirements: cloud.Requirements{
            GPUModels: []string{"GPU"}, Regions: []string{"west"},
            MaxComputeHourlyPrice: &cloud.Money{Amount: "2", Currency: "CNY"},
            MaxStorageDailyPrice:  &cloud.Money{Amount: "1", Currency: "CNY"},
        }, AutoDL: cloud.ImageOptions{Compatibility: &cloud.ImageCompatibility{CUDA: "12.6"}}, Clone: &cloud.CloneSource{Region: "west"}},
    }
    expected := cloneCloudPlan(d.plan)
    d.rememberApproval()
    exact := client.CloudApproval{Operation: "acquire", Account: "account", Product: "standard", Target: "gpu", Plan: &expected}
    if !d.approved.matches(exact) {
        t.Fatal("unchanged approved plan was rejected")
    }
    for _, change := range []func(*client.CloudApproval){
        func(a *client.CloudApproval) { a.Account = "other" },
        func(a *client.CloudApproval) { a.Product = "pro" },
        func(a *client.CloudApproval) { a.Target = "other" },
        func(a *client.CloudApproval) { a.Operation = "start" },
    } {
        altered := exact
        change(&altered)
        if d.approved.matches(altered) {
            t.Fatal("changed identity reused approval", altered)
        }
    }
    d.plan.Requirements.GPUModels[0] = "other"
    d.plan.Requirements.Regions[0] = "other"
    d.plan.Requirements.MaxComputeHourlyPrice.Amount = "99"
    d.plan.Requirements.MaxStorageDailyPrice.Amount = "99"
    d.plan.AutoDL.Compatibility.CUDA = "99"
    d.plan.Clone.Region = "other"
    if !reflect.DeepEqual(d.approved.plan, expected) {
        t.Fatal("editable references changed the approval snapshot", d.approved.plan)
    }
    changed := exact
    changed.Plan = &d.plan
    if d.approved.matches(changed) {
        t.Fatal("changed plan reused approval")
    }
}

func TestCloudDraftInvalidatesDerivedSelectionButKeepsExplicitInput(t *testing.T) {
    d := cloudDraft{offset: 100, selected: &cloud.Offer{ID: "old"}, approved: &cloudApproved{},
        in: client.CloudAcquireInput{OfferID: "old", Plan: client.CloudPlanInput{InventoryOffset: 100, ImageName: "old", Compatibility: &cloud.ImageCompatibility{}, Explicit: cloud.Profile{GPUModels: []string{}, AutoDL: cloud.AutoDLProfile{Image: cloudPointer("image"), ExpandDataDiskGB: cloudPointer(0)}}}},
    }
    d.invalidateCandidate()
    if d.selected != nil || d.approved != nil || d.offset != 0 || d.in.OfferID != "" || d.in.Plan.InventoryOffset != 0 || d.in.Plan.Compatibility != nil || d.in.Plan.ImageName != "" {
        t.Fatal(d)
    }
    if d.in.Plan.Explicit.GPUModels == nil || d.in.Plan.Explicit.AutoDL.Image == nil || *d.in.Plan.Explicit.AutoDL.Image != "image" || d.in.Plan.Explicit.AutoDL.ExpandDataDiskGB == nil {
        t.Fatal("invalidation lost explicit empty/zero/input", d.in.Plan)
    }
    d.models, d.regions = []cloud.CatalogItem{{ID: "old"}}, []cloud.CatalogItem{{ID: "old"}}
    d.invalidateScope()
    if d.models != nil || d.regions != nil || d.in.Plan.Explicit.AutoDL.Image != nil {
        t.Fatal("old account/product cache survived", d)
    }
}

func TestCloudWizardKeepsParentGrammarUnchanged(t *testing.T) {
    o, provider := uxFixture(t)
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    model := &grammar{}
    parser, err := newParser(model)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := parser.Parse([]string{"-i", "cloud", "acquire"}); err != nil {
        t.Fatal(err)
    }
    before := *model
    answers := []string{"", "1", "", "", "", "", "2", "", "", "", "", "", "", "", "yes"}
    input := strings.NewReader(strings.Join(answers, "\n") + "\n")
    c := &invocation{ui: true, app: client.New(o.Dependencies, settings), options: o.Options, grammar: model,
        output: &presenter{command: "cloud acquire", locale: i18n.English(), out: o.Out, err: o.Err},
        input:  interaction.New(interaction.Options{Interactive: true, In: input, Err: o.Err}),
    }
    data, err := c.acquireCloud(context.Background())
    if err != nil || provider.creates != 1 {
        t.Fatal(data, err, provider.creates, o.Err)
    }
    if !reflect.DeepEqual(*model, before) {
        t.Fatal("wizard modified the parent command grammar")
    }
    if err := c.cloudNavigation("cloud acquire", data, nil); err != nil {
        t.Fatal(err)
    }
    if c.output.cloudContext == nil || c.output.cloudContext.Account != "main" || c.output.cloudContext.Product != "standard" || c.output.cloudContext.Target != "gpu" {
        t.Fatal("wizard selection was not passed to presentation", c.output.cloudContext)
    }
}
