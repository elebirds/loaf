package cloud

import (
    "context"
    "errors"
    "io"
    "testing"
    "time"
)

func TestContinuousAcquisitionSurvivesReadBackoffAndCreatesOnce(t *testing.T) {
    p := &fakeStock{fakeProvider: &fakeProvider{}}
    s, in := serviceFixture(t, p)
    in.Plan.Continuous = true
    in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "2.50", Currency: "CNY"}
    waits := 0
    s.Wait = func(context.Context, time.Duration) error { waits++; return nil }
    p.search = func(n int) (InventoryPage, error) {
        if n <= 6 {
            return InventoryPage{}, &APIError{Code: "TRANSPORT", Transient: true}
        }
        if n == 7 {
            return InventoryPage{Offers: []Offer{}, Complete: true}, nil
        }
        return InventoryPage{Offers: []Offer{{ID: "host", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "2.00", Currency: "CNY"}}}, Complete: true}, nil
    }
    events := []Event{}
    result, err := s.Acquire(context.Background(), in, func(e Event) error { events = append(events, e); return nil })
    if err != nil || p.creates != 1 || result.Attempts != 1 || waits != 7 || !result.Deadline.IsZero() {
        t.Fatal(result, err, p.creates, waits)
    }
    saved, err := s.Receipts.Load(result.ID)
    if err != nil || !saved.Plan.Continuous || !saved.Deadline.IsZero() {
        t.Fatal(saved, err)
    }
    polls, attempts := 0, 0
    for _, e := range events {
        if e.Type == "poll" {
            polls++
            if e.NextCheckAt == nil {
                t.Fatal("missing real next check")
            }
        }
        if e.Type == "created" {
            attempts = e.Attempts
        }
    }
    if polls != 7 || attempts != 1 {
        t.Fatal(events)
    }
}

func TestAcceptedPowerRetainsReceiptWhenProgressConsumerFails(t *testing.T) {
    s, in := serviceFixture(t, &fakeProvider{})
    ref := Ref{Provider: "autodl", Account: in.Account, Product: "pro", ID: "existing"}
    r, err := s.Power(context.Background(), Binding{Ref: ref, Settings: in.Settings}, false, time.Minute, func(Event) error { return io.ErrClosedPipe })
    if errorCode(err) != "CLOUD_UNCONFIRMED" || r.Status != "accepted" || r.Instance == nil || *r.Instance != ref {
        t.Fatal(r, err)
    }
    saved, e := s.Receipts.Load(r.ID)
    if e != nil || saved.Status != "accepted" || saved.Attempts != 1 {
        t.Fatal(saved, e)
    }
}

func TestContinuousRequoteReadFailureResumesOnlyBeforeMutation(t *testing.T) {
    for _, continuous := range []bool{false, true} {
        p := &fakeStock{fakeProvider: &fakeProvider{}}
        s, in := serviceFixture(t, p)
        in.Plan.Continuous = continuous
        in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "3.00", Currency: "CNY"}
        waits := 0
        s.Wait = func(context.Context, time.Duration) error { waits++; return nil }
        p.create = func() (Ref, error) {
            if p.creates == 1 {
                return Ref{}, &APIError{Code: "TRANSPORT", Transient: true, BeforeMutation: true}
            }
            return Ref{Provider: "autodl", Product: "pro", Account: "demo", ID: "one-instance"}, nil
        }
        r, err := s.Acquire(context.Background(), in, nil)
        if continuous {
            if err != nil || r.Status != "created" || r.Instance == nil || r.Instance.ID != "one-instance" || waits != 1 || p.creates != 2 {
                t.Fatal(r, err, waits, p.creates)
            }
        } else if err == nil || waits != 0 || p.creates != 1 {
            t.Fatal(r, err, waits, p.creates)
        }
    }
}

type pinnedStock struct {
    *fakeStock
    picked string
}

func (p *pinnedStock) Create(_ context.Context, _ Plan, o *Offer) (Ref, error) {
    p.picked = o.ID
    p.creates++
    return Ref{Provider: "autodl", Account: "demo", Product: "pro", ID: "created"}, nil
}

func TestPinnedOfferNeverSubstitutesCheapestOrDisappearedHost(t *testing.T) {
    for _, disappears := range []bool{false, true} {
        t.Run(map[bool]string{false: "selected", true: "disappeared"}[disappears], func(t *testing.T) {
            p := &pinnedStock{fakeStock: &fakeStock{fakeProvider: &fakeProvider{}}}
            s, in := serviceFixture(t, p)
            in.Plan.OfferID = "wanted"
            in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "2.50", Currency: "CNY"}
            p.search = func(n int) (InventoryPage, error) {
                rows := []Offer{{ID: "cheap", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "1.00", Currency: "CNY"}}}
                if !disappears || n == 1 {
                    rows = append(rows, Offer{ID: "wanted", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "2.00", Currency: "CNY"}})
                }
                return InventoryPage{Offers: rows, Complete: true}, nil
            }
            _, err := s.Acquire(context.Background(), in, nil)
            if disappears {
                if errorCode(err) != "NO_MATCH" || p.creates != 0 {
                    t.Fatal(err, p.picked)
                }
            } else if err != nil || p.picked != "wanted" {
                t.Fatal(err, p.picked)
            }
        })
    }
}

func TestContinuousCancellationAndUnknownMutationRetainOriginalIdentity(t *testing.T) {
    p := &fakeStock{fakeProvider: &fakeProvider{}}
    s, in := serviceFixture(t, p)
    in.Plan.Continuous = true
    in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "3.00", Currency: "CNY"}
    p.create = func() (Ref, error) { return Ref{}, context.Canceled }
    result, err := s.Acquire(context.Background(), in, nil)
    if errorCode(err) != "CLOUD_UNCONFIRMED" || result.ID == "" || p.creates != 1 {
        t.Fatal(result, err)
    }
    in.Target = "another"
    if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "CLOUD_UNCONFIRMED" || p.creates != 1 {
        t.Fatal(err, p.creates)
    }
    p2 := &fakeStock{fakeProvider: &fakeProvider{}, search: func(int) (InventoryPage, error) { return InventoryPage{Offers: []Offer{}, Complete: true}, nil }}
    s2, in2 := serviceFixture(t, p2)
    in2.Plan.Continuous = true
    in2.Plan.Requirements.MaxComputeHourlyPrice = in.Plan.Requirements.MaxComputeHourlyPrice
    ctx, cancel := context.WithCancel(context.Background())
    s2.Wait = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
    r, e := s2.Acquire(ctx, in2, nil)
    if !errors.Is(e, context.Canceled) || p2.creates != 0 || r.ID == "" {
        t.Fatal(r, e)
    }
}

func TestNewAcquisitionSafetyAndStorageConditions(t *testing.T) {
    p := Plan{Product: "standard", WaitTimeout: "0s", Continuous: true, Requirements: Requirements{GPUCount: 1}, AutoDL: ImageOptions{Image: "image"}}
    if ValidateAcquisition(p, "session") == nil {
        t.Fatal("continuous mode without cap")
    }
    p.Requirements.MaxComputeHourlyPrice = &Money{Amount: "2.00", Currency: "CNY"}
    p.AutoDL.ExpandDataDiskGB = 50
    if ValidateAcquisition(p, "session") == nil {
        t.Fatal("expansion without daily cap")
    }
    p.Requirements.MaxStorageDailyPrice = &Money{Amount: "1.00", Currency: "CNY"}
    if err := ValidateAcquisition(p, "session"); err != nil {
        t.Fatal(err)
    }
    o := Offer{GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: p.Requirements.MaxComputeHourlyPrice}
    if len(Match(o, p.Requirements)) == 0 {
        t.Fatal("unknown storage price passed")
    }
}

func TestContinuousInventoryWaitDoesNotBlockDifferentInstancePower(t *testing.T) {
    p := &fakeStock{fakeProvider: &fakeProvider{}, search: func(int) (InventoryPage, error) { return InventoryPage{Offers: []Offer{}, Complete: true}, nil }}
    s, in := serviceFixture(t, p)
    in.Plan.Continuous = true
    in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "2.00", Currency: "CNY"}
    waiting := make(chan struct{})
    s.Wait = func(ctx context.Context, _ time.Duration) error { close(waiting); <-ctx.Done(); return ctx.Err() }
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    done := make(chan error, 1)
    go func() { _, err := s.Acquire(ctx, in, nil); done <- err }()
    select {
    case <-waiting:
    case err := <-done:
        t.Fatal("acquisition ended before waiting", err)
    case <-time.After(10 * time.Second):
        t.Fatal("acquisition never waited")
    }
    powerCtx, powerCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer powerCancel()
    r, err := s.Power(powerCtx, Binding{Ref: Ref{Provider: "autodl", Account: in.Account, Product: "pro", ID: "existing"}, Settings: in.Settings}, false, 0)
    if err != nil || r.Status != "accepted" {
        t.Fatal(r, err)
    }
    cancel()
    if err := <-done; !errors.Is(err, context.Canceled) {
        t.Fatal(err)
    }
}
