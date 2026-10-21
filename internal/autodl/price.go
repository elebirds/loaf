package autodl

import (
    "context"
    "encoding/json"

    "github.com/elebirds/loaf/internal/cloud"
)

// quotedPrices follows the console's final discount entries. Values already
// cover all requested GPUs: instance fees are hourly, disk fees are daily.
func quotedPrices(raw json.RawMessage, instance, disk string) (*cloud.Money, *cloud.Money, error) {
    var data struct {
        Discounts []object `json:"discount_list"`
    }
    if json.Unmarshal(raw, &data) != nil || data.Discounts == nil {
        return nil, nil, &cloud.APIError{Code: "INVALID_RESPONSE"}
    }
    var compute, storage *cloud.Money
    for _, item := range data.Discounts {
        product := text(item, "product_type")
        if product != instance && product != disk {
            continue
        }
        origin, reduction := integer(item, "origin_price"), integer(item, "total_reduction")
        if origin == nil || reduction == nil || *origin < *reduction {
            return nil, nil, &cloud.APIError{Code: "INVALID_RESPONSE"}
        }
        value, err := cloud.MilliCNY(int64(*origin-*reduction), 1)
        if err != nil {
            return nil, nil, &cloud.APIError{Code: "INVALID_RESPONSE"}
        }
        if product == instance {
            compute = value
        } else {
            storage = value
        }
    }
    return compute, storage, nil
}

func (s *Standard) quote(ctx context.Context, plan cloud.Plan, machine string) (*cloud.Money, *cloud.Money, error) {
    body := map[string]any{"charge_type": "payg", "coupon_id_list": []string{}, "duration": 1, "expand_data_disk": int64(plan.AutoDL.ExpandDataDiskGB) << 30, "machine_id": machine, "num": plan.Requirements.GPUCount}
    raw, err := s.request(ctx, "POST", "/api/v1/order/price/preview", body, false)
    if err != nil {
        return nil, nil, err
    }
    return quotedPrices(raw, "instance", "data_disk")
}

func checkPrice(price *cloud.Money, r cloud.Requirements) error {
    if price == nil {
        return &cloud.APIError{Code: "PRICE_UNVERIFIABLE"}
    }
    if r.MaxComputeHourlyPrice != nil && (price.Currency != r.MaxComputeHourlyPrice.Currency || cloud.Less(*r.MaxComputeHourlyPrice, *price)) {
        return &cloud.APIError{Code: "OFFER_CHANGED"}
    }
    return nil
}
