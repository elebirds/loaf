package cli

import (
    "context"
    "errors"
    "fmt"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/i18n"
)

func cloudProduct(l *i18n.Localizer, product string) string {
    if product == "standard" || product == "pro" {
        return l.Text("cloud_ui_product_" + product)
    }
    return product
}

func cloudScope(l *i18n.Localizer, account, product, target string) string {
    parts := []string{}
    for _, s := range []string{target, account, cloudProduct(l, product)} {
        if s != "" {
            parts = append(parts, s)
        }
    }
    return strings.Join(parts, " · ")
}

func cloudPlanRows(l *i18n.Localizer, p cloud.Plan, regionCatalog []cloud.CatalogItem) []interaction.Row {
    row := func(id, value string) interaction.Row {
        return interaction.Row{Label: l.Text("cloud_ui_label_" + id), Value: value}
    }
    models := strings.Join(p.Requirements.GPUModels, " / ")
    if models == "" {
        models = l.Text("cloud_ui_any_gpu")
    }
    rows := []interaction.Row{row("gpu", fmt.Sprintf("%s × %d", models, p.Requirements.GPUCount))}
    if p.OfferID != "" {
        rows = append(rows, row("offer", p.OfferID))
    }
    regions := cloudRegionSummary(regionCatalog, p.Requirements.Regions)
    if regions == "" {
        regions = l.Text("cloud_ui_any_region")
    }
    rows = append(rows, row("region", regions))
    price := l.Text("cloud_price_unknown")
    if cap := p.Requirements.MaxComputeHourlyPrice; cap != nil {
        price = l.Text("cloud_ui_hourly_cap", map[string]any{"Value": cap.Amount})
    }
    rows = append(rows, row("budget", price))
    if cap := p.Requirements.MaxStorageDailyPrice; cap != nil {
        rows = append(rows, row("storage_price", l.Text("cloud_ui_daily_cap", map[string]any{"Value": cap.Amount})))
    }
    image := p.AutoDL.Name
    if image == "" {
        image = p.AutoDL.Image
        if p.AutoDL.PrivateImageUUID != "" {
            image = p.AutoDL.PrivateImageUUID
        }
    }
    if p.Clone != nil {
        image = p.Clone.Image
        rows = append(rows, row("source", p.Clone.Instance.ID))
    }
    if image != "" {
        rows = append(rows, row("image", image))
    }
    if p.AutoDL.ExpandDataDiskGB > 0 {
        rows = append(rows, row("disk", l.Text("cloud_ui_data_disk", map[string]any{"Count": p.AutoDL.ExpandDataDiskGB})))
    }
    if p.AutoDL.ExpandSystemDiskGB > 0 {
        rows = append(rows, row("disk", l.Text("cloud_ui_system_disk", map[string]any{"Count": p.AutoDL.ExpandSystemDiskGB})))
    }
    limits := []string{}
    for _, f := range []struct {
        id  string
        n   int
    }{{"gpu_memory", p.Requirements.MinGPUMemoryGiB}, {"cpus", p.Requirements.MinCPUs}, {"memory", p.Requirements.MinMemoryGiB}, {"disk", p.Requirements.MinDiskGiB}} {
        if f.n > 0 {
            limits = append(limits, l.Text("cloud_ui_min_"+f.id, map[string]any{"Count": f.n}))
        }
    }
    if len(limits) > 0 {
        rows = append(rows, row("minimum", strings.Join(limits, " · ")))
    }
    if p.Requirements.GPUVendor != "" {
        rows = append(rows, row("vendor", p.Requirements.GPUVendor))
    }
    if p.AutoDL.GPUSpecID > 0 {
        rows = append(rows, row("spec", fmt.Sprint(p.AutoDL.GPUSpecID)))
    }
    if p.AutoDL.GPUSpecUUID != "" {
        rows = append(rows, row("spec", p.AutoDL.GPUSpecUUID))
    }
    waiting := p.WaitTimeout
    if p.Continuous {
        waiting = l.Text("cloud_ui_wait_continuous")
    } else if waiting == "0s" {
        waiting = l.Text("cloud_ui_wait_once")
    }
    return append(rows, row("wait", waiting))
}

func cloudCoverage(l *i18n.Localizer, n int, complete bool) string {
    id := "cloud_ui_coverage_partial"
    if complete {
        id = "cloud_ui_coverage_complete"
    }
    return l.Text(id, map[string]any{"Count": n})
}

// cloudBusy is a transient view of a read, not a second polling loop.
func (c *invocation) cloudBusy(ctx context.Context, title, scope string, read func(context.Context) error) (err error) {
    if !c.input.Rich() {
        if _, e := fmt.Fprintln(c.output.err, title); e != nil {
            return e
        }
        return read(ctx)
    }
    work, cancel := context.WithCancel(ctx)
    defer cancel()
    frame := interaction.ProgressFrame{Document: interaction.Document{Title: title, Context: scope}, Footer: c.output.locale.Text("cloud_ui_cancel")}
    live, err := c.input.StartProgress(work, cancel, frame)
    if err != nil {
        return err
    }
    defer func() {
        if e := live.Close(); e != nil && err == nil {
            err = e
        }
    }()
    return read(work)
}

// finishCloud owns one human result. JSON and explicit inventory/details keep
// their existing contracts, while lifecycle results no longer recursively dump receipts.
func (p *presenter) finishCloud(data any, e *errorView) (bool, error) {
    switch p.command {
    case "cloud acquire", "cloud bind", "cloud start", "cloud stop", "cloud reconcile":
    default:
        return false, nil
    }
    l := p.locale
    d := interaction.Document{Tone: "warning"}
    var receipt *cloud.Receipt
    var ref *cloud.Ref
    target := ""
    ready := false
    known := false
    cloneStatus := ""
    var actionErr error
    displayAction := func(action uxNextAction) string {
        display, err := uxJCommandDisplay(action.Command)
        if err != nil {
            actionErr = errors.Join(actionErr, err)
            return p.error(err).Message
        }
        return display
    }
    switch r := data.(type) {
    case cloud.Plan:
        if e == nil {
            return false, nil
        }
        d.Title = e.Message
        if p.cloudContext != nil {
            d.Context = cloudScope(l, p.cloudContext.Account, r.Product, p.cloudContext.Target)
        }
        if p.cloudDeclined {
            d.Title = l.Text("cloud_ui_cancelled")
            d.Body = l.Text("cloud_ui_not_sent")
        }
        if !p.cloudDeclined && e.Hint != "" {
            d.Body = e.Hint
        }
        if len(p.nextActions) > 0 {
            if d.Body != "" {
                d.Body += "\n\n"
            }
            d.Body += l.Text("cloud_ui_next") + "\n" + displayAction(p.nextActions[0])
        }
        if _, err := fmt.Fprintln(p.out, d.Render(p.width, p.styled, p.dark)); err != nil {
            p.writeErr = err
        }
        return true, actionErr
    case client.CloudBinding:
        target, ready, cloneStatus = r.Target, r.Status == "ready", r.CloneDataStatus
        known = r.Status == "ready" || r.Status == "bound" || r.Request != nil && r.Request.Instance != nil
        if r.Instance.ID != "" {
            ref = &r.Instance
        }
        receipt = r.Request
    case client.CloudAcquisition:
        receipt = &r.Receipt
        ref = r.Instance
        known = ref != nil
    case cloud.Receipt:
        receipt = &r
        ref = r.Instance
        known = ref != nil
    default:
        return false, nil
    }
    if receipt != nil && target == "" {
        target = receipt.Target
    }
    account, product := "", ""
    if ref != nil {
        account, product = ref.Account, ref.Product
    }
    if receipt != nil && account == "" {
        account, product = receipt.Account, receipt.Plan.Product
    }
    d.Context = cloudScope(l, account, product, target)
    if ref != nil {
        d.Rows = append(d.Rows, interaction.Row{Label: l.Text("cloud_ui_label_instance"), Value: ref.ID})
    }
    operation := ""
    if receipt != nil {
        operation = receipt.Operation
        if operation == "" && (p.command == "cloud start" || p.command == "cloud stop") {
            operation = strings.TrimPrefix(p.command, "cloud ")
        }
    }
    switch {
    case ready:
        d.Title = l.Text("cloud_ui_result_ready", map[string]any{"Target": target})
        d.Tone = "success"
        if e != nil {
            d.Title = l.Text("cloud_ui_result_ready_partial", map[string]any{"Target": target})
            d.Tone = "warning"
            d.Body = e.Message
        }
    case receipt != nil && (operation == "start" || operation == "stop"):
        state := receipt.Status
        if state != "completed" && state != "accepted" && state != "rejected" {
            state = "unconfirmed"
        }
        d.Title = l.Text("cloud_ui_result_" + operation + "_" + state)
        if state == "completed" {
            d.Tone = "success"
        }
        if state == "accepted" {
            d.Body = l.Text("cloud_ui_power_observe")
        }
        if e != nil {
            d.Body = e.Message
        }
    case receipt != nil && receipt.Status == "not_created" && e == nil:
        d.Title = l.Text("cloud_ui_result_not_created")
    case receipt != nil && (receipt.Status == "uncertain" || receipt.Status == "intent"):
        d.Title = l.Text("cloud_ui_result_uncertain")
        d.Body = l.Text("cloud_ui_check_console")
    case ref != nil:
        d.Title = l.Text("cloud_ui_result_bind")
        d.Body = l.Text("cloud_ui_connect_recovery")
        if known {
            d.Title = l.Text("cloud_ui_result_connect")
        }
        if cloneStatus == "copying" {
            d.Title = l.Text("cloud_ui_result_copying")
            d.Body = l.Text("cloud_ui_copy_recovery")
        }
        if cloneStatus == "failed" {
            d.Title = l.Text("cloud_ui_result_copy_failed")
            d.Body = l.Text("cloud_ui_copy_failed_recovery")
        }
        if e != nil && e.Code != "CLOUD_NOT_READY" && e.Code != "CLONE_FAILED" {
            d.Body = e.Message
        }
        if known {
            d.Body += "\n" + l.Text("cloud_ui_billing_active")
        }
    default:
        if e == nil {
            return false, nil
        }
        d.Title = e.Message
    }
    if d.Title == "" {
        return false, nil
    }
    if e != nil && e.Detail != "" {
        if d.Body != "" {
            d.Body += "\n"
        }
        d.Body += e.Detail
    }
    if receipt != nil && receipt.ID != "" && ref == nil {
        d.Rows = append(d.Rows, interaction.Row{Label: l.Text("cloud_ui_label_request"), Value: receipt.ID})
    }
    if len(p.nextActions) > 0 {
        a := p.nextActions[0]
        label := "cloud_ui_next"
        if a.Kind == "resume_binding" {
            label = "cloud_ui_resume"
        } else if a.Kind == "inspect_request" {
            label = "cloud_ui_inspect"
        } else if a.Kind == "select_machine" {
            label = "cloud_ui_use"
        }
        action := l.Text(label) + "\n" + displayAction(a)
        if d.Body != "" {
            d.Body += "\n\n"
        }
        d.Body += action
    }
    if e != nil && e.Hint != "" && !(known && (e.Code == "CLOUD_NOT_READY" || e.Code == "CLONE_FAILED")) && (receipt == nil || receipt.Status != "intent" && receipt.Status != "uncertain") {
        if d.Body != "" {
            d.Body += "\n\n"
        }
        d.Body += e.Hint
    }
    if _, err := fmt.Fprintln(p.out, d.Render(p.width, p.styled, p.dark)); err != nil {
        p.writeErr = err
    }
    return true, actionErr
}
