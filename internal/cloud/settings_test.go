package cloud

import (
    "reflect"
    "testing"
)

func ptr[T any](v T) *T { return &v }

func TestResolveResourceLayersAndSafety(t *testing.T) {
    p, err := Resolve(Ceiling{MaxComputeHourlyPrice: "2.50", Currency: "CNY"},
        Profile{GPUCount: ptr(2), MinMemoryGiB: ptr(64), GPUModels: []string{"A", "B"}, Regions: []string{"west"}, MaxComputeHourlyPrice: ptr("1.00"), WaitTimeout: ptr("30m"), AutoDL: AutoDLProfile{ExpandDataDiskGB: ptr(32), Image: ptr("base")}},
        Profile{MinMemoryGiB: ptr(32), GPUModels: []string{"C"}, MaxComputeHourlyPrice: ptr("9.99")},
        Profile{MinMemoryGiB: ptr(0), Regions: []string{}, WaitTimeout: ptr("0s"), AutoDL: AutoDLProfile{ExpandDataDiskGB: ptr(0)}},
    )
    if err != nil || p.Requirements.GPUCount != 2 || p.Requirements.MinMemoryGiB != 0 || p.WaitTimeout != "0s" || p.AutoDL.ExpandDataDiskGB != 0 || p.AutoDL.Image != "base" || !reflect.DeepEqual(p.Requirements.GPUModels, []string{"C"}) || len(p.Requirements.Regions) != 0 || p.Requirements.MaxComputeHourlyPrice.Amount != "2.50" {
        t.Fatal(p, err)
    }
    cleared, err := Resolve(Ceiling{MaxComputeHourlyPrice: "0", Currency: "CNY"}, Profile{MaxComputeHourlyPrice: ptr("")})
    if err != nil || cleared.Requirements.MaxComputeHourlyPrice == nil || cleared.Requirements.MaxComputeHourlyPrice.Amount != "0" {
        t.Fatal(cleared, err)
    }
    for _, profile := range []Profile{{GPUCount: ptr(0)}, {MinMemoryGiB: ptr(-1)}, {PollInterval: ptr("1s")}, {WaitTimeout: ptr("25h")}, {Currency: ptr("USD")}, {MaxComputeHourlyPrice: ptr("NaN")}, {GPUModels: []string{""}}} {
        if _, err := Resolve(Ceiling{}, profile); err == nil {
            t.Fatal("accepted invalid profile", profile)
        }
    }
}

func TestExactMoneyAndRequiredUnknowns(t *testing.T) {
    for _, value := range []string{"-1", "1e3", "NaN", "1.0000001", "9999999999", ".5", "1.", "+1"} {
        if _, err := ParseMoney(value, "CNY"); err == nil {
            t.Fatal(value)
        }
    }
    a, _ := ParseMoney("0.100001", "CNY")
    b, _ := ParseMoney("0.100002", "CNY")
    if !Less(a, b) || Less(b, a) {
        t.Fatal(a, b)
    }
    price, err := MilliCNY(1251, 2)
    if err != nil || price.Amount != "2.502" {
        t.Fatal(price, err)
    }
    p, _ := Resolve(Ceiling{}, Profile{GPUCount: ptr(2), MinGPUMemoryGiB: ptr(24), MinMemoryGiB: ptr(32), MaxComputeHourlyPrice: ptr("2.50")})
    offer := Offer{ID: "a", GPUCount: 2, Available: ptr(true), GPUMemoryGiB: ptr(16), ComputeHourlyPrice: price}
    want := []string{"gpu_memory", "memory_unknown", "price"}
    if got := Match(offer, p.Requirements); !reflect.DeepEqual(got, want) {
        t.Fatal(got)
    }
    offer.GPUMemoryGiB, offer.MemoryGiB, offer.ComputeHourlyPrice = ptr(24), ptr(32), &Money{Amount: "2.499999", Currency: "CNY"}
    if got := Match(offer, p.Requirements); len(got) != 0 {
        t.Fatal(got)
    }
    offer.Available = nil
    if got := Match(offer, p.Requirements); !reflect.DeepEqual(got, []string{"availability_unknown"}) {
        t.Fatal(got)
    }
}

func TestSelectRanksAndPreservesCoverage(t *testing.T) {
    p, _ := Resolve(Ceiling{})
    page := InventoryPage{Examined: 3, Complete: false, Offers: []Offer{
        {ID: "z", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "2.5", Currency: "CNY"}},
        {ID: "a", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "2.499", Currency: "CNY"}},
        {ID: "bad", GPUCount: 1},
    }}
    r := Select(page, p)
    if len(r.Matches) != 2 || r.Matches[0].ID != "a" || len(r.Rejected["bad"]) != 1 || r.Complete || r.Examined != 3 {
        t.Fatal(r)
    }
}
