package cli

import (
    "reflect"
    "slices"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
)

type cloudStage uint8

const (
    cloudCompute cloudStage = iota
    cloudCandidate
    cloudEnvironment
    cloudReview
    cloudDone
)

type cloudTransition uint8

const (
    cloudNext cloudTransition = iota
    cloudBack
    cloudStay
    cloudCancel
    cloudRestart
    cloudRetryCandidate
    cloudStartReview
)

func cloudNavigation(value string) cloudTransition {
    switch value {
    case "back":
        return cloudBack
    case "cancel":
        return cloudCancel
    default:
        return cloudNext
    }
}

// move is the single owner of wizard stage and approval transitions. Pages
// return navigation outcomes rather than rewriting the stage behind the driver.
func (d *cloudDraft) move(next cloudTransition) {
    if next != cloudNext || d.stage != cloudReview {
        d.approved = nil
    }
    switch next {
    case cloudNext:
        if d.stage < cloudDone {
            d.stage++
        }
    case cloudBack:
        if d.stage > cloudCompute {
            d.stage--
        }
    case cloudRestart:
        d.stage = cloudCompute
    case cloudRetryCandidate:
        d.stage = cloudCandidate
    case cloudStartReview:
        d.stage = cloudReview
    }
}

// invalidateCandidate clears derived selection state when its requirements change.
// Explicit user inputs remain inputs; the following pages resolve them again.
func (d *cloudDraft) invalidateCandidate() {
    d.selected, d.in.OfferID, d.offset, d.in.Plan.InventoryOffset = nil, "", 0, 0
    d.in.Plan.Compatibility, d.in.Plan.ImageName = nil, ""
    d.approved = nil
}

func (d *cloudDraft) invalidateScope() {
    d.invalidateCandidate()
    d.models, d.regions = nil, nil
    d.in.Plan.Explicit.Regions = nil
    d.in.Plan.Explicit.AutoDL = cloud.AutoDLProfile{}
    d.in.Plan.Profile = ""
    d.environmentReset = false
}

func (d *cloudDraft) chooseCandidate(offer cloud.Offer) {
    if d.selected == nil || d.selected.ID != offer.ID || d.selected.Vendor != offer.Vendor || d.selected.Architecture != offer.Architecture {
        d.in.Plan.Compatibility, d.in.Plan.ImageName = nil, ""
    }
    d.approved = nil
    d.selected = &offer
    d.in.OfferID, d.in.Plan.InventoryOffset = offer.ID, d.offset
}

// cloudApproved is independent of editable input and catalog-owned references.
type cloudApproved struct {
    account, product, target string
    plan                     cloud.Plan
}

func copyCloudValue[T any](value *T) *T {
    if value == nil {
        return nil
    }
    result := *value
    return &result
}

func cloneCloudPlan(plan cloud.Plan) cloud.Plan {
    plan.Requirements.GPUModels = slices.Clone(plan.Requirements.GPUModels)
    plan.Requirements.Regions = slices.Clone(plan.Requirements.Regions)
    plan.Requirements.MaxComputeHourlyPrice = copyCloudValue(plan.Requirements.MaxComputeHourlyPrice)
    plan.Requirements.MaxStorageDailyPrice = copyCloudValue(plan.Requirements.MaxStorageDailyPrice)
    plan.AutoDL.Compatibility = copyCloudValue(plan.AutoDL.Compatibility)
    plan.Clone = copyCloudValue(plan.Clone)
    return plan
}

func (d *cloudDraft) rememberApproval() {
    d.approved = &cloudApproved{account: *d.in.Select.Account, product: d.plan.Product, target: d.in.Name, plan: cloneCloudPlan(d.plan)}
}

func (a *cloudApproved) matches(approval client.CloudApproval) bool {
    return a != nil && approval.Operation == "acquire" && approval.Account == a.account && approval.Product == a.product && approval.Target == a.target && approval.Plan != nil && reflect.DeepEqual(*approval.Plan, a.plan)
}

func (d *cloudDraft) present(c *invocation) {
    account := ""
    if d.in.Select.Account != nil {
        account = *d.in.Select.Account
    }
    c.output.cloudContext = &cloudContext{Account: account, Product: d.plan.Product, Target: d.in.Name}
}
