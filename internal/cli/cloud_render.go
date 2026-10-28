package cli

import (
    "fmt"
    "io"
    "sort"
    "strings"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/i18n"
)

func printCloud(out io.Writer, data any, l *i18n.Localizer) bool {
    switch r := data.(type) {
    case loginResult:
        fmt.Fprintln(out, l.Text("cloud_login_done", map[string]any{"Account": r.Account, "Username": r.Identity.Username}))
    case logoutResult:
        fmt.Fprintln(out, l.Text("cloud_logout_done", map[string]any{"Account": r.Account}))
    case cloudAccounts:
        names := make([]string, 0, len(r.Accounts))
        for name := range r.Accounts {
            names = append(names, name)
        }
        sort.Strings(names)
        for _, name := range names {
            a := r.Accounts[name]
            mark := ""
            if r.Default == name {
                mark = " *"
            }
            location := a.Credential.Env
            if location == "" {
                location = a.Credential.Store + ":" + a.Credential.Key
            }
            fmt.Fprintf(out, "%s%s  %s/%s  %s  %s\n", name, mark, a.Provider, a.DefaultProduct, a.Credential.Mode, location)
        }
        if len(names) == 0 {
            fmt.Fprintln(out, l.Text("cloud_accounts_empty"))
        }
    case cloud.Page:
        for _, v := range r.Instances {
            printCloud(out, v, l)
        }
        fmt.Fprintln(out, l.Text("cloud_coverage", map[string]any{"Count": r.Examined, "Value": r.Complete}))
    case cloud.Instance:
        fmt.Fprintf(out, "%s  %s/%s  %s  %s\n", r.Ref.ID, r.Ref.Account, r.Ref.Product, l.Text("cloud_status_"+r.Status), r.Region)
        if r.CloneDataStatus != "" {
            fmt.Fprintln(out, l.Text("cloud_clone_data_"+r.CloneDataStatus))
        }
    case cloud.Ref:
        fmt.Fprintf(out, "%s  %s/%s\n", r.ID, r.Account, r.Product)
    case cloudCredentials:
        printCloud(out, r.Instance, l)
        fmt.Fprintf(out, "ssh -p %d %s@%s\n", r.Connection.Port, r.Connection.User, r.Connection.Host)
        if r.Password != nil {
            fmt.Fprintln(out, *r.Password)
        }
    case cloudBinding:
        fmt.Fprintln(out, l.Text("cloud_binding", map[string]any{"Target": r.Target, "ID": r.Instance.ID, "Value": l.Text("cloud_status_" + r.Status)}))
        if r.CloneDataStatus != "" {
            fmt.Fprintln(out, l.Text("cloud_clone_data_"+r.CloneDataStatus))
        }
        if r.Request != nil {
            printCloud(out, *r.Request, l)
        }
        if r.Status == "ready" {
            fmt.Fprintln(out, l.Text("cloud_binding_next", map[string]any{"Target": r.Target}))
        }
    case cloud.Receipt:
        if r.ID != "" {
            fmt.Fprintf(out, "%s  %s  %s\n", r.ID, r.Operation, l.Text("cloud_status_"+r.Status))
        }
        if r.Instance != nil {
            printCloud(out, *r.Instance, l)
        }
    case cloudAcquisition:
        printCloud(out, r.Receipt, l)
        if r.Observation != nil {
            printCloud(out, *r.Observation, l)
        }
    case cloudReceipts:
        for _, v := range r.Requests {
            printCloud(out, v, l)
        }
        if len(r.Requests) == 0 {
            fmt.Fprintln(out, l.Text("cloud_requests_empty"))
        }
    case cloud.Plan:
        req := r.Requirements
        price := l.Text("cloud_price_unknown")
        if req.MaxComputeHourlyPrice != nil {
            price = req.MaxComputeHourlyPrice.Amount + " " + req.MaxComputeHourlyPrice.Currency
        }
        fmt.Fprintln(out, l.Text("cloud_plan_summary", map[string]any{"Count": req.GPUCount, "Value": price, "Mode": r.WaitTimeout}))
        if len(req.GPUModels) > 0 {
            fmt.Fprintln(out, strings.Join(req.GPUModels, ", "))
        }
        if req.GPUVendor != "" {
            fmt.Fprintln(out, "--gpu-vendor", req.GPUVendor)
        }
        fmt.Fprintf(out, "--min-gpu-memory %dGiB  --min-cpus %d  --min-memory %dGiB  --min-disk %dGiB\n", req.MinGPUMemoryGiB, req.MinCPUs, req.MinMemoryGiB, req.MinDiskGiB)
        if len(req.Regions) > 0 {
            fmt.Fprintln(out, "--region", strings.Join(req.Regions, ","))
        }
        if r.Clone != nil {
            fmt.Fprintln(out, l.Text("cloud_clone_summary", map[string]any{"ID": r.Clone.Instance.ID, "Region": r.Clone.Region}))
        } else {
            fmt.Fprintln(out, "--image", r.AutoDL.Image)
        }
        if r.AutoDL.GPUSpecID != 0 {
            fmt.Fprintln(out, "--gpu-spec-id", r.AutoDL.GPUSpecID)
        }
        if r.AutoDL.CUDAFrom != 0 {
            fmt.Fprintln(out, "--cuda-from", r.AutoDL.CUDAFrom)
        }
        if r.AutoDL.GPUSpecUUID != "" {
            fmt.Fprintln(out, "--gpu-spec", r.AutoDL.GPUSpecUUID)
        }
        if r.AutoDL.ExpandSystemDiskGB > 0 {
            fmt.Fprintln(out, "--expand-system-disk", r.AutoDL.ExpandSystemDiskGB)
        }
        if r.AutoDL.ExpandDataDiskGB > 0 {
            fmt.Fprintln(out, "--expand-data-disk", r.AutoDL.ExpandDataDiskGB)
        }
        fmt.Fprintln(out, l.Text("cloud_billing_warning"))
    case cloud.SearchResult:
        printCloud(out, r.Plan, l)
        fmt.Fprintln(out, l.Text("cloud_matches", map[string]any{"Count": len(r.Matches)}))
        for _, offer := range r.Matches {
            price := l.Text("cloud_price_unknown")
            if offer.ComputeHourlyPrice != nil {
                price = offer.ComputeHourlyPrice.Amount + " " + offer.ComputeHourlyPrice.Currency
            }
            fmt.Fprintf(out, "%s  %s  %s  %s\n", offer.ID, offer.Model, offer.Region, price)
            if offer.StorageDailyPrice != nil {
                fmt.Fprintln(out, l.Text("cloud_storage_daily", map[string]any{"Price": offer.StorageDailyPrice.Amount, "Currency": offer.StorageDailyPrice.Currency}))
            }
        }
        reasons := map[string]int{}
        for _, row := range r.Rejected {
            for _, reason := range row {
                reasons[reason]++
            }
        }
        names := make([]string, 0, len(reasons))
        for name := range reasons {
            names = append(names, name)
        }
        sort.Strings(names)
        for _, name := range names {
            fmt.Fprintf(out, "  %s: %d\n", l.Text("cloud_reject_"+name), reasons[name])
        }
        if !r.ObservedAt.IsZero() {
            fmt.Fprintln(out, l.Text("cloud_observed", map[string]any{"Value": r.ObservedAt.Format("2006-01-02T15:04:05Z07:00")}))
        }
        fmt.Fprintln(out, l.Text("cloud_coverage", map[string]any{"Count": r.Examined, "Value": r.Complete}))
    default:
        return false
    }
    return true
}
