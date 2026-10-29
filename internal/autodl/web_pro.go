package autodl

import (
    "context"
    "encoding/json"
    "net/url"
    "slices"
    "strconv"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
)

// WebPro uses the main/subaccount session, not Pro's developer API. Catalog,
// quote and order shapes follow the console's instance-pro and pay-type bundles.
// Catalog observations are not reservations; unknown hardware remains unknown.
type WebPro struct{ *api }

func webProID(id int, model, region string) string {
    return strconv.Itoa(id) + ":" + url.QueryEscape(model) + ":" + url.QueryEscape(region)
}
func parseWebProID(offer *cloud.Offer) (int, error) {
    if offer == nil {
        return 0, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    fields := strings.Split(offer.ID, ":")
    if len(fields) != 3 {
        return 0, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    id, err := strconv.Atoi(fields[0])
    model, e1 := url.QueryUnescape(fields[1])
    region, e2 := url.QueryUnescape(fields[2])
    if err != nil || id <= 0 || e1 != nil || e2 != nil || model != offer.Model || region != offer.Region {
        return 0, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    return id, nil
}

func proPriceBody(plan cloud.Plan, spec int) map[string]any {
    return map[string]any{"charge_type": "payg", "duration": 1, "expand_system_disk": int64(30+plan.AutoDL.ExpandSystemDiskGB) << 30, "num": plan.Requirements.GPUCount, "gpu_spec_info": map[string]int{"id": spec}, "coupon_id_list": []string{}}
}

// quote separates the instance's hourly compute fee from daily disk charges.
// Both are whole-request values, already including the requested GPU count.
func (p *WebPro) quote(ctx context.Context, plan cloud.Plan, spec int, model string) (*cloud.Money, *cloud.Money, error) {
    body := proPriceBody(plan, spec)
    body["gpu_name"] = model
    raw, err := p.request(ctx, "POST", "/api/v1/order/price/preview/pro", body, false)
    if err != nil {
        return nil, nil, err
    }
    return quotedPrices(raw, "instance_pro", "system_disk")
}

// Search bounds examined spec/region/model combinations, with one quote per
// eligible combination. A numeric web spec ID is not a developer spec UUID.
func (p *WebPro) Search(ctx context.Context, plan cloud.Plan, limit int) (cloud.InventoryPage, error) {
    result := cloud.InventoryPage{Offers: []cloud.Offer{}, ObservedAt: time.Now().UTC()}
    if limit < 1 || limit > 1000 || plan.AutoDL.GPUSpecUUID != "" || plan.Requirements.GPUCount < 1 || plan.Requirements.GPUCount > 8 {
        return result, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    regions := plan.Requirements.Regions
    if len(regions) == 0 {
        // The console exposes Pro in these two data centers and requires a
        // nonempty selection. Query each separately to retain stock locality.
        regions = []string{"westDC3", "beijingDC2"}
    }
    for _, region := range regions {
        selectedRegions := []string{}
        if region != "" {
            selectedRegions = append(selectedRegions, region)
        }
        raw, err := p.request(ctx, "POST", "/api/v1/machine_pro/gpu_type", map[string]any{"data_center_list": selectedRegions}, false)
        if err != nil {
            return result, err
        }
        var models []object
        if json.Unmarshal(raw, &models) != nil || models == nil || len(models) > 1000 {
            return result, &cloud.APIError{Code: "INVALID_RESPONSE"}
        }
        for _, model := range models {
            name := text(model, "gpu_type")
            if name == "" || strings.ContainsAny(name, "\x00\r\n") {
                return result, &cloud.APIError{Code: "INVALID_RESPONSE"}
            }
            if len(plan.Requirements.GPUModels) > 0 && !slices.Contains(plan.Requirements.GPUModels, name) {
                continue
            }
            raw, err = p.request(ctx, "POST", "/api/v1/machine_pro/gpu_spec_config", map[string]string{"gpu_name": name, "charge_type": "payg"}, false)
            if err != nil {
                return result, err
            }
            var specs []object
            if json.Unmarshal(raw, &specs) != nil || specs == nil || len(specs) > 1000 {
                return result, &cloud.APIError{Code: "INVALID_RESPONSE"}
            }
            for _, spec := range specs {
                if result.Examined >= limit {
                    return result, nil
                }
                result.Examined++
                id := integer(spec, "id")
                if id == nil || *id <= 0 {
                    return result, &cloud.APIError{Code: "INVALID_RESPONSE"}
                }
                if plan.AutoDL.GPUSpecID != 0 && plan.AutoDL.GPUSpecID != *id {
                    continue
                }
                offer := cloud.Offer{ID: webProID(*id, name, region), Model: name, Region: region, GPUCount: plan.Requirements.GPUCount, Source: "autodl:pro:web:catalog+quote", ObservedAt: result.ObservedAt}
                if idle := integer(model, "gpu_idle_num"); idle != nil {
                    available := *idle >= offer.GPUCount
                    offer.Available = &available
                }
                // The spec exposes a floor/range, not the final allocation.
                // Using the unmultiplied floor is safe whether per card or total.
                offer.CPUs = integer(spec, "cpu_num_from")
                offer.MemoryGiB = allocated(spec, "memory_size_from", 1, 1<<30)
                disk := 30 + plan.AutoDL.ExpandSystemDiskGB
                offer.DiskGiB = &disk
                eligible := plan.Requirements
                eligible.MaxComputeHourlyPrice = nil
                eligible.MaxStorageDailyPrice = nil
                if len(cloud.Match(offer, eligible)) == 0 {
                    offer.ComputeHourlyPrice, offer.StorageDailyPrice, err = p.quote(ctx, plan, *id, name)
                    if err != nil {
                        return result, err
                    }
                }
                result.Offers = append(result.Offers, offer)
            }
        }
    }
    result.Complete = true
    return result, nil
}

func (p *WebPro) Create(ctx context.Context, plan cloud.Plan, offer *cloud.Offer) (cloud.Ref, error) {
    spec, err := parseWebProID(offer)
    image := plan.AutoDL
    if err != nil || image.GPUSpecUUID != "" || image.GPUSpecID != 0 && image.GPUSpecID != spec || image.CUDAFrom <= 0 || plan.Requirements.GPUCount < 1 || plan.Requirements.GPUCount > 8 || image.ExpandDataDiskGB != 0 || image.ExpandSystemDiskGB < 0 || image.ExpandSystemDiskGB > 500 || (image.Image == "") == (image.PrivateImageUUID == "") || image.Image != "" && !validID.MatchString(image.Image) || image.PrivateImageUUID != "" && !validID.MatchString(image.PrivateImageUUID) {
        return cloud.Ref{}, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    // Quote again immediately before the one mutating call. A rejected or failed
    // quote is read-only and never makes a possibly duplicated order.
    compute, storage, err := p.quote(ctx, plan, spec, offer.Model)
    if err != nil {
        return cloud.Ref{}, creationReadFailure(err)
    }
    if compute == nil {
        return cloud.Ref{}, &cloud.APIError{Code: "PRICE_UNVERIFIABLE"}
    }
    checked := *offer
    checked.ComputeHourlyPrice = compute
    checked.StorageDailyPrice = storage
    if len(cloud.Match(checked, plan.Requirements)) > 0 {
        return cloud.Ref{}, &cloud.APIError{Code: "OFFER_CHANGED"}
    }
    regions := []string{}
    if offer.Region != "" {
        regions = append(regions, offer.Region)
    }
    body := map[string]any{
        "instance_info": map[string]any{"charge_type": "payg", "req_gpu_amount": plan.Requirements.GPUCount, "instance_name": "", "machine_cuda_v": image.CUDAFrom, "data_center_list": regions, "expand_system_disk": int64(30+image.ExpandSystemDiskGB) << 30, "gpu_spec_info": map[string]int{"id": spec}, "base_image_uuid": image.Image, "private_image_uuid": image.PrivateImageUUID, "reproduction_uuid": "", "reproduction_id": 0},
        "price_info":    proPriceBody(plan, spec),
    }
    raw, err := p.request(ctx, "POST", "/api/v1/order/instance/pro/create/payg", body, true)
    return created(p.api, raw, err)
}

var _ cloud.Provider = (*WebPro)(nil)
var _ cloud.Inventory = (*WebPro)(nil)
