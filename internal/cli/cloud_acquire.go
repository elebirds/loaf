package cli

import (
    "context"
    "errors"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "sync/atomic"
)

func (c *invocation) acquireInput() (client.CloudAcquireInput, error) {
    g, v := c.grammar.Cloud, c.grammar.Cloud.Acquire
    plan, err := v.ResourceFlags.plan(g.Product)
    offer, offset, decodeErr := decodeOfferSelection(v.Offer)
    if decodeErr != nil {
        return client.CloudAcquireInput{}, fault.Invalid("--offer", "invalid_value", "cloud_config")
    }
    plan.InventoryOffset = offset
    plan.Explicit.Continuous = v.Continuous
    return client.CloudAcquireInput{Select: client.CloudSelect{Account: g.Account, Product: g.Product}, Plan: plan, Name: v.Name, Limit: v.Limit, CloneFrom: v.CloneFrom, Yes: v.Yes, AllowUnquoted: v.AllowUnquoted, RemoteBase: v.RemoteDir, ReadyTimeout: v.ReadyTimeout, Notify: v.Notify, OfferID: offer, DryRun: v.DryRun}, err
}

func acquisitionData(result client.AcquisitionResult) any {
    if result.Preview != nil {
        return *result.Preview
    }
    if result.Binding != nil {
        return *result.Binding
    }
    if result.Acquisition != nil {
        return *result.Acquisition
    }
    if result.Plan != nil {
        return *result.Plan
    }
    return nil
}

func (c *invocation) acquireOnce(ctx context.Context, in client.CloudAcquireInput, approvals ...client.ApproveCloud) (result client.AcquisitionResult, err error) {
    runCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    feedback := &cloudFeedback{call: c, mode: c.grammar.Cloud.Acquire.Progress, cancel: cancel, ctx: runCtx}
    defer func() {
        if e := feedback.close(); e != nil && err == nil {
            err = e
        }
    }()
    var approval client.ApproveCloud
    if c.interactiveInput() {
        approval = c.approveCloud
    }
    if len(approvals) > 0 {
        approval = approvals[0]
    }
    return c.app.Resources.CloudAcquire(runCtx, in, approval, feedback.emit)
}

func (c *invocation) acquireCloud(ctx context.Context) (any, error) {
    in, err := c.acquireInput()
    if err != nil {
        return nil, err
    }
    wizard := c.interactiveInput() && !in.Yes && !in.DryRun
    if !wizard {
        if in.Name == "" {
            return nil, fault.Invalid("name", "missing_argument", "cloud_name_required")
        }
        result, e := c.acquireOnce(ctx, in)
        return acquisitionData(result), e
    }
    c.output.cloudWizard = true
    draft := &cloudDraft{in: in}
    if err := draft.setup(ctx, c); err != nil {
        return nil, err
    }
    for {
        if err := draft.edit(ctx, c); err != nil {
            if errors.Is(err, interaction.ErrCanceled) {
                c.output.cloudDeclined = true
                draft.present(c)
                return draft.plan, err
            }
            return nil, err
        }
        draft.present(c)
        editRequested := &atomic.Bool{}
        c.editAcquisition = editRequested
        result, e := c.acquireOnce(ctx, draft.in, func(work context.Context, approval client.CloudApproval) error {
            return draft.approve(work, c, approval)
        })
        c.editAcquisition = nil
        if editRequested.Load() && canEditCloudAcquisition(result) {
            draft.move(cloudRestart)
            continue
        }
        var f *fault.Error
        if draft.mode == "select" && errors.As(e, &f) && f.Code == "NO_MATCH" {
            choice, er := draft.selectChoice(ctx, c, "cloud_candidate_changed", draft.navigation(c, "retry"), "back")
            if er != nil {
                return acquisitionData(result), er
            }
            if choice == "next" {
                draft.move(cloudRetryCandidate)
                continue
            }
            if choice == "back" {
                draft.move(cloudRestart)
                continue
            }
        }
        return acquisitionData(result), e
    }
}

func canEditCloudAcquisition(result client.AcquisitionResult) bool {
    if result.Binding != nil || result.Acquisition == nil {
        return false
    }
    receipt := result.Acquisition.Receipt
    if receipt.Instance != nil {
        return false
    }
    switch receipt.Status {
    case "not_created", "no_stock", "rejected":
        return true
    }
    return false
}
