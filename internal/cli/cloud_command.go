package cli

import (
    "encoding/base64"
    "encoding/csv"
    "strconv"
    "strings"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/ssh"
)

func shellCommand(argv []string) string {
    words := make([]string, len(argv))
    for i, arg := range argv {
        words[i] = ssh.Quote(arg)
    }
    return strings.Join(words, " ")
}

func cloudCSV(values []string) string {
    if len(values) == 0 {
        return ""
    }
    var b strings.Builder
    w := csv.NewWriter(&b)
    _ = w.Write(values)
    w.Flush()
    return strings.TrimSuffix(b.String(), "\n")
}

// cloudPlanCommand freezes public effective conditions, not payment authority.
func cloudPlanCommand(in client.CloudAcquireInput, p client.CloudPreview) []string {
    plan, r := p.Plan, p.Plan.Requirements
    args := []string{"loaf", "cloud", "acquire", p.Target}
    add := func(flag, value string) { args = append(args, flag+"="+value) }
    add("--account", p.Account)
    add("--product", plan.Product)
    add("--gpu-model", cloudCSV(r.GPUModels))
    add("--gpu-vendor", r.GPUVendor)
    add("--gpu-count", strconv.Itoa(r.GPUCount))
    for _, v := range []struct {
        flag string
        n    int
    }{{"--min-gpu-memory", r.MinGPUMemoryGiB}, {"--min-memory", r.MinMemoryGiB}, {"--min-disk", r.MinDiskGiB}} {
        add(v.flag, strconv.Itoa(v.n)+"GiB")
    }
    add("--min-cpus", strconv.Itoa(r.MinCPUs))
    price, storage := "", ""
    if r.MaxComputeHourlyPrice != nil {
        price = r.MaxComputeHourlyPrice.Amount
    }
    if r.MaxStorageDailyPrice != nil {
        storage = r.MaxStorageDailyPrice.Amount
    }
    add("--max-compute-hourly-price", price)
    add("--max-storage-daily-price", storage)
    add("--currency", "CNY")
    add("--poll-interval", plan.PollInterval)
    add("--ready-timeout", in.ReadyTimeout.String())
    add("--limit", strconv.Itoa(in.Limit))
    add("--remote-dir", in.RemoteBase)
    if in.OfferID != "" {
        add("--offer", encodeOfferSelection(in.OfferID, in.Plan.InventoryOffset))
    } else {
        add("--continuous", strconv.FormatBool(plan.Continuous))
        if !plan.Continuous {
            add("--wait", plan.WaitTimeout)
        }
    }
    if in.CloneFrom != nil {
        add("--clone-from", *in.CloneFrom)
        if in.Plan.Explicit.Regions != nil {
            add("--region", cloudCSV(in.Plan.Explicit.Regions))
        }
        if in.Plan.Explicit.AutoDL.ExpandDataDiskGB != nil {
            add("--expand-data-disk", strconv.Itoa(*in.Plan.Explicit.AutoDL.ExpandDataDiskGB))
        }
    } else {
        add("--region", cloudCSV(r.Regions))
        add("--image", plan.AutoDL.Image)
        add("--private-image", plan.AutoDL.PrivateImageUUID)
        add("--expand-data-disk", strconv.Itoa(plan.AutoDL.ExpandDataDiskGB))
        add("--expand-system-disk", strconv.Itoa(plan.AutoDL.ExpandSystemDiskGB))
        add("--cuda-from", strconv.Itoa(plan.AutoDL.CUDAFrom))
        add("--gpu-spec", plan.AutoDL.GPUSpecUUID)
        add("--gpu-spec-id", strconv.Itoa(plan.AutoDL.GPUSpecID))
    }
    if in.AllowUnquoted {
        add("--allow-unquoted", "true")
    }
    return args
}

// Paging hints are discovery locations, never authorization or reservations.
// Receipts keep the canonical ID; this hint only lives in copied CLI input.
func encodeOfferSelection(id string, offset int) string {
    if offset == 0 {
        return id
    }
    return "loaf-offer-v1:" + strconv.Itoa(offset) + ":" + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func decodeOfferSelection(value string) (string, int, error) {
    if !strings.HasPrefix(value, "loaf-offer-v1:") {
        return value, 0, nil
    }
    parts := strings.Split(strings.TrimPrefix(value, "loaf-offer-v1:"), ":")
    if len(parts) != 2 {
        return "", 0, parseFailure()
    }
    offset, err := strconv.Atoi(parts[0])
    if err != nil || offset < 0 || offset > 1000000 {
        return "", 0, parseFailure()
    }
    data, err := base64.RawURLEncoding.DecodeString(parts[1])
    if err != nil || len(data) == 0 || len(data) > 512 {
        return "", 0, parseFailure()
    }
    return string(data), offset, nil
}

func cloudInputIssues(plan cloud.Plan, mode string, clone bool) []inputIssue {
    issues := []inputIssue{}
    add := func(argument, reason string) {
        for _, i := range issues {
            if i.Argument == argument {
                return
            }
        }
        issues = append(issues, inputIssue{Argument: argument, Reason: reason})
    }
    if plan.OfferID != "" && (plan.Continuous || plan.WaitTimeout != "0s") {
        add("--offer", "conflicting_arguments")
    }
    if (plan.Continuous || plan.OfferID != "") && plan.Requirements.MaxComputeHourlyPrice == nil {
        add("--max-compute-hourly-price", "missing_argument")
    }
    if (plan.Continuous || plan.OfferID != "") && (plan.AutoDL.ExpandDataDiskGB > 0 || plan.AutoDL.ExpandSystemDiskGB > 0) && plan.Requirements.MaxStorageDailyPrice == nil {
        add("--max-storage-daily-price", "missing_argument")
    }
    if !clone {
        if plan.AutoDL.Image == "" && plan.AutoDL.PrivateImageUUID == "" {
            add("--image", "missing_argument")
        }
        if plan.AutoDL.Image != "" && plan.AutoDL.PrivateImageUUID != "" {
            add("--image", "conflicting_arguments")
        }
        if plan.Product == "pro" && plan.AutoDL.CUDAFrom <= 0 {
            add("--cuda-from", "missing_argument")
        }
        if plan.Product == "pro" && mode == "developer" && plan.AutoDL.GPUSpecUUID == "" {
            add("--gpu-spec", "missing_argument")
        }
    }
    return issues
}
