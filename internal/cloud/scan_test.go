package cloud

import (
    "context"
    "reflect"
    "testing"
    "time"
)

type pagedStock struct {
    *fakeStock
    offsets []int
    windows func(int) InventoryPage
}

func (p *pagedStock) SearchWindow(_ context.Context, _ Plan, limit, offset int) (InventoryPage, error) {
    if limit != 100 {
        panic("limit changed")
    }
    p.offsets = append(p.offsets, offset)
    return p.windows(offset), nil
}
func TestPagedAcquisitionRotatesAndRechecksSameWindow(t *testing.T) {
    p := &pagedStock{fakeStock: &fakeStock{fakeProvider: &fakeProvider{}}}
    p.windows = func(offset int) InventoryPage {
        if offset == 0 {
            return InventoryPage{Examined: 100, HasMore: true, NextOffset: 100}
        }
        return InventoryPage{Examined: 1, ScanOffset: 100, Offers: []Offer{{ID: "later-host", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "2", Currency: "CNY"}}}}
    }
    s, in := serviceFixture(t, p)
    in.Plan.Continuous = true
    in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "3", Currency: "CNY"}
    s.Wait = func(context.Context, time.Duration) error { return nil }
    receipt, err := s.Acquire(context.Background(), in, nil)
    if err != nil || receipt.Offer == nil || receipt.Offer.ID != "later-host" || p.creates != 1 || !reflect.DeepEqual(p.offsets, []int{0, 100, 100}) {
        t.Fatal(receipt, err, p.offsets)
    }
}
func TestPinnedLaterWindowNeverSubstitutesOtherOffer(t *testing.T) {
    p := &pagedStock{fakeStock: &fakeStock{fakeProvider: &fakeProvider{}}}
    reads := 0
    p.windows = func(offset int) InventoryPage {
        reads++
        id := "wanted"
        if reads > 1 {
            id = "replacement"
        }
        return InventoryPage{Examined: 1, ScanOffset: offset, Offers: []Offer{{ID: id, GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "2", Currency: "CNY"}}}}
    }
    s, in := serviceFixture(t, p)
    in.Plan.OfferID = "wanted"
    in.Plan.InventoryOffset = 150
    in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "3", Currency: "CNY"}
    receipt, err := s.Acquire(context.Background(), in, nil)
    if errorCode(err) != "NO_MATCH" || p.creates != 0 || receipt.Instance != nil || !reflect.DeepEqual(p.offsets, []int{150, 150}) {
        t.Fatal(receipt, err, p.offsets)
    }
}
func TestPagedWatchWrapsWithoutClaimingSnapshot(t *testing.T) {
    p := &pagedStock{fakeStock: &fakeStock{fakeProvider: &fakeProvider{}}}
    p.windows = func(offset int) InventoryPage {
        if offset == 0 {
            return InventoryPage{Examined: 100, HasMore: true, NextOffset: 100}
        }
        return InventoryPage{Examined: 1, ScanOffset: 100}
    }
    s, in := serviceFixture(t, p)
    in.Plan.WaitTimeout = "1h"
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    s.Wait = func(context.Context, time.Duration) error {
        if len(p.offsets) >= 3 {
            cancel()
        }
        return ctx.Err()
    }
    _, _ = s.Watch(ctx, p, in.Plan, 100, nil)
    if !reflect.DeepEqual(p.offsets, []int{0, 100, 0}) {
        t.Fatal(p.offsets)
    }
}
