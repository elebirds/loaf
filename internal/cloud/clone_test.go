package cloud

import (
    "context"
    "errors"
    "reflect"
    "testing"
    "time"
)

func sourceFixture() CloneSource {
    return CloneSource{Instance: Ref{Provider: "autodl", Product: "standard", Account: "demo", ID: "old-instance"}, Region: "west-D", Image: "old-image", Vendor: "nvidia", Architecture: "x86", CUDA: "12.8", ExpandDataDiskGB: 150}
}

func clonePlan(t *testing.T) Plan {
    t.Helper()
    plan, err := Resolve(Ceiling{}, Profile{Product: ptr("standard"), MaxComputeHourlyPrice: ptr("3.00"), AutoDL: AutoDLProfile{ExpandDataDiskGB: ptr(150)}})
    if err != nil {
        t.Fatal(err)
    }
    plan, err = PrepareClone(plan, sourceFixture())
    if err != nil {
        t.Fatal(err)
    }
    return plan
}

func TestClonePolicyCannotWidenRegionOrShrinkDisk(t *testing.T) {
    source := sourceFixture()
    for _, tc := range []struct {
        name   string
        change func(*Plan)
        want   string
    }{
        {"inherit", func(*Plan) {}, ""},
        {"same region", func(p *Plan) { p.Requirements.Regions = []string{"west-D"} }, ""},
        {"foreign region", func(p *Plan) { p.Requirements.Regions = []string{"west-X"} }, "BAD_CONFIG"},
        {"widened regions", func(p *Plan) { p.Requirements.Regions = []string{"west-D", "west-X"} }, "BAD_CONFIG"},
        {"smaller disk", func(p *Plan) { p.AutoDL.ExpandDataDiskGB = 0 }, "BAD_CONFIG"},
        {"different vendor", func(p *Plan) { p.Requirements.GPUVendor = "amd" }, "BAD_CONFIG"},
    } {
        t.Run(tc.name, func(t *testing.T) {
            plan := clonePlan(t)
            plan.Clone = nil
            plan.Requirements.Regions = nil
            plan.AutoDL.Image = "configured-unrelated-image"
            tc.change(&plan)
            got, err := PrepareClone(plan, source)
            if errorCode(err) != tc.want {
                t.Fatal(got, err)
            }
            if err == nil && (got.Clone == nil || *got.Clone != source || got.AutoDL.Image != "" || !reflect.DeepEqual(got.Requirements.Regions, []string{"west-D"})) {
                t.Fatal(got)
            }
        })
    }
}

type cloneStock struct {
    *fakeStock
    clones int
    clone  func(Plan, *Offer) (Ref, error)
    region string
}

func (p *cloneStock) CloneSource(context.Context, Ref) (CloneSource, error) {
    return sourceFixture(), nil
}
func (p *cloneStock) Clone(_ context.Context, plan Plan, offer *Offer) (Ref, error) {
    p.clones++
    return p.clone(plan, offer)
}
func (p *cloneStock) Get(_ context.Context, r Ref) (Instance, error) {
    return Instance{Ref: r, Status: "running", Region: p.region}, nil
}

func cloneServiceFixture(t *testing.T) (*Service, AcquireInput, *cloneStock) {
    t.Helper()
    p := &cloneStock{fakeStock: &fakeStock{fakeProvider: &fakeProvider{}}, region: "west-D"}
    s, in := serviceFixture(t, p)
    in.Plan = clonePlan(t)
    in.Settings.DefaultProduct = "standard"
    in.Settings.Credential.Mode = "session"
    p.search = func(int) (InventoryPage, error) {
        return InventoryPage{Complete: true, Offers: []Offer{
            {ID: "foreign", Region: "west-X", Vendor: "nvidia", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "0.10", Currency: "CNY"}},
            {ID: "local", Region: "west-D", Vendor: "nvidia", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "2.78", Currency: "CNY"}},
        }}, nil
    }
    p.clone = func(Plan, *Offer) (Ref, error) {
        return Ref{Provider: "autodl", Product: "standard", Account: "demo", ID: "new-clone"}, nil
    }
    return s, in, p
}

func TestCloneUsesNativePollingAndIntentWithoutCreatingBlankInstance(t *testing.T) {
    s, in, p := cloneServiceFixture(t)
    in.Plan.WaitTimeout = "1m"
    baseSearch := p.search
    p.search = func(n int) (InventoryPage, error) {
        if n == 1 {
            return InventoryPage{Complete: true}, nil
        }
        return baseSearch(n)
    }
    waits := 0
    s.Wait = func(context.Context, time.Duration) error { waits++; return nil }
    baseClone := p.clone
    p.clone = func(plan Plan, o *Offer) (Ref, error) {
        rows, err := s.Receipts.List()
        if err != nil || len(rows) != 1 || rows[0].Status != "intent" || rows[0].Plan.Clone == nil || *rows[0].Plan.Clone != sourceFixture() || o.Region != "west-D" {
            t.Fatal(rows, err, o)
        }
        if p.clones == 1 {
            return Ref{}, &APIError{Code: "NO_STOCK", NoStock: true}
        }
        return baseClone(plan, o)
    }
    r, err := s.Acquire(context.Background(), in, nil)
    if err != nil || p.creates != 0 || p.clones != 2 || waits != 2 || r.Attempts != 2 || r.Instance.ID != "new-clone" {
        t.Fatal(r, err, p.creates, p.clones, waits)
    }
    saved, err := s.Receipts.Load(r.ID)
    if err != nil || saved.Plan.Clone == nil || saved.Plan.Clone.Instance.ID != "old-instance" {
        t.Fatal(saved, err)
    }
}

func TestCloneUnconfirmedNeverReplaysAndReconcileRetainsRegion(t *testing.T) {
    for _, cause := range []error{&APIError{Code: "TRANSPORT", Unconfirmed: true}, context.Canceled} {
        s, in, p := cloneServiceFixture(t)
        p.clone = func(Plan, *Offer) (Ref, error) { return Ref{}, cause }
        r, err := s.Acquire(context.Background(), in, nil)
        if errorCode(err) != "CLOUD_UNCONFIRMED" || p.clones != 1 || p.creates != 0 {
            t.Fatal(r, err)
        }
        in.Target = "other"
        if _, err = s.Acquire(context.Background(), in, nil); errorCode(err) != "CLOUD_UNCONFIRMED" || p.clones != 1 {
            t.Fatal(err)
        }
        p.region = "west-X"
        if _, err = s.Reconcile(context.Background(), r.ID, "wrong-region", false); errorCode(err) != "BAD_CONFIG" {
            t.Fatal(err)
        }
        p.region = "west-D"
        if _, err = s.Reconcile(context.Background(), r.ID, "old-instance", false); errorCode(err) != "BAD_CONFIG" {
            t.Fatal(err)
        }
        saved, err := s.Reconcile(context.Background(), r.ID, "new-clone", false)
        if err != nil || saved.Instance.ID != "new-clone" || saved.Plan.Clone.Instance.ID != "old-instance" || p.clones != 1 {
            t.Fatal(saved, err)
        }
    }
}

func TestCloneCancellationAndUnsupportedProviderDoNotCreate(t *testing.T) {
    s, in, p := cloneServiceFixture(t)
    p.search = func(int) (InventoryPage, error) { return InventoryPage{Complete: true}, nil }
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    in.Plan.WaitTimeout = "1m"
    s.Wait = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
    if _, err := s.Acquire(ctx, in, nil); !errors.Is(err, context.Canceled) || p.clones != 0 || p.creates != 0 {
        t.Fatal(err)
    }
    s, in, p = cloneServiceFixture(t)
    s.Open = func(context.Context, string, Account, string) (Provider, error) { return p.fakeStock, nil }
    if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "UNSUPPORTED_CAPABILITY" || p.creates != 0 {
        t.Fatal(err)
    }
}

func TestConcurrentCloneAcquisitionHasOneOwner(t *testing.T) {
    s, in, p := cloneServiceFixture(t)
    entered, release := make(chan struct{}), make(chan struct{})
    defer close(release)
    p.clone = func(Plan, *Offer) (Ref, error) {
        close(entered)
        <-release
        return Ref{}, &APIError{Code: "TRANSPORT", Unconfirmed: true}
    }
    done := make(chan error, 1)
    go func() { _, err := s.Acquire(context.Background(), in, nil); done <- err }()
    <-entered
    other := in
    other.Target = "other-target"
    if _, err := s.Acquire(context.Background(), other, nil); errorCode(err) != "CLOUD_BUSY" || p.clones != 1 || p.creates != 0 {
        t.Fatal(err, p.clones, p.creates)
    }
    // Complete the owner before returning; an unexpected failure must not leave
    // another goroutine using this test's temporary receipt directory.
    release <- struct{}{}
    if err := <-done; errorCode(err) != "CLOUD_UNCONFIRMED" {
        t.Fatal(err)
    }
}
