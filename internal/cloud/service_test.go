package cloud

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "sync/atomic"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/fault"
)

type fakeProvider struct {
    creates int
    gets    int
    starts  int
    create  func() (Ref, error)
}

func (p *fakeProvider) List(context.Context, int) (Page, error) { return Page{}, nil }
func (p *fakeProvider) Get(_ context.Context, r Ref) (Instance, error) {
    p.gets++
    return Instance{Ref: r, Status: "running"}, nil
}
func (p *fakeProvider) Connection(context.Context, Ref) (Connection, error) { return Connection{}, nil }
func (p *fakeProvider) Create(context.Context, Plan, *Offer) (Ref, error) {
    p.creates++
    if p.create != nil {
        return p.create()
    }
    return Ref{Provider: "autodl", Product: "pro", Account: "demo", ID: "pro-created"}, nil
}
func (p *fakeProvider) Start(context.Context, Ref) error { p.starts++; return nil }
func (p *fakeProvider) Stop(context.Context, Ref) error  { return nil }

type fakeStock struct {
    *fakeProvider
    searches int
    search   func(int) (InventoryPage, error)
}

func (p *fakeStock) Search(_ context.Context, _ Plan, _ int) (InventoryPage, error) {
    p.searches++
    if p.search != nil {
        return p.search(p.searches)
    }
    return InventoryPage{Offers: []Offer{{ID: "machine", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: "1.99", Currency: "CNY"}}}, Examined: 1, Complete: true}, nil
}

func serviceFixture(t *testing.T, p Provider) (*Service, AcquireInput) {
    t.Helper()
    plan, _ := Resolve(Ceiling{}, Profile{AutoDL: AutoDLProfile{Image: ptr("image-fixture"), GPUSpecUUID: ptr("spec"), CUDAFrom: ptr(118)}})
    plan.Product = "pro"
    return &Service{Open: func(context.Context, string, Account, string) (Provider, error) { return p, nil }, Receipts: Receipts{Dir: filepath.Join(t.TempDir(), "cloud")}}, AcquireInput{Account: "demo", Settings: Account{Provider: "autodl", DefaultProduct: "pro", Credential: CredentialRef{Mode: "developer", Env: "TEST_TOKEN"}}, Target: "gpu", RemoteBase: "~/projects", Plan: plan, Limit: 100, AllowUnquoted: true}
}

func errorCode(err error) string {
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return ""
}

func TestAcquireIntentBeforeChargeAndUnknownNeverReplays(t *testing.T) {
    for _, cause := range []error{&APIError{Code: "TRANSPORT", Unconfirmed: true}, &APIError{Code: "INVALID_RESPONSE", HTTPStatus: 500, Unconfirmed: true}, context.Canceled} {
        p := &fakeProvider{}
        s, in := serviceFixture(t, p)
        p.create = func() (Ref, error) {
            rows, err := s.Receipts.List()
            if err != nil || len(rows) != 1 || rows[0].Status != "intent" || rows[0].Attempts != 1 {
                t.Fatal(rows, err)
            }
            return Ref{}, cause
        }
        r, err := s.Acquire(context.Background(), in, nil)
        if errorCode(err) != "CLOUD_UNCONFIRMED" || p.creates != 1 || r.Status != "uncertain" {
            t.Fatal(r, err, p.creates)
        }
        // Neither a new alias nor a second command may evade the account guard.
        in.Target = "replacement"
        if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "CLOUD_UNCONFIRMED" || p.creates != 1 {
            t.Fatal(err, p.creates)
        }
        saved, err := s.Receipts.Load(r.ID)
        if err != nil || saved.Status != "uncertain" {
            t.Fatal(saved, err)
        }
        reconciled, err := s.Reconcile(context.Background(), r.ID, "pro-known", false)
        if err != nil || reconciled.Status != "created" || reconciled.Instance.ID != "pro-known" || p.creates != 1 {
            t.Fatal(reconciled, err)
        }
    }
}

func TestAcquireRetriesOnlyConfirmedNoStock(t *testing.T) {
    p := &fakeProvider{}
    s, in := serviceFixture(t, p)
    in.Plan.WaitTimeout = "1m"
    waits := 0
    s.Wait = func(_ context.Context, d time.Duration) error {
        waits++
        if d < 35*time.Second {
            t.Fatal("ignored no-stock Retry-After", d)
        }
        return nil
    }
    p.create = func() (Ref, error) {
        if p.creates == 1 {
            return Ref{}, &APIError{Code: "NO_STOCK", NoStock: true, RetryAfter: 35 * time.Second}
        }
        return Ref{Provider: "autodl", Product: "pro", Account: "demo", ID: "pro-only-one"}, nil
    }
    r, err := s.Acquire(context.Background(), in, nil)
    if err != nil || r.Instance.ID != "pro-only-one" || r.Attempts != 2 || p.creates != 2 || waits != 1 {
        t.Fatal(r, err, p.creates, waits)
    }
    if err := s.MarkReady(context.Background(), r.ID, *r.Instance); err != nil {
        t.Fatal(err)
    }
    saved, _ := s.Receipts.Load(r.ID)
    if saved.Status != "ready" {
        t.Fatal(saved)
    }
}

func TestProPriceAndUnknownHardwareRejectBeforeCharge(t *testing.T) {
    p := &fakeProvider{}
    s, in := serviceFixture(t, p)
    cap, _ := ParseMoney("2.50", "CNY")
    in.Plan.Requirements.MaxComputeHourlyPrice = &cap
    if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "PRICE_UNVERIFIABLE" || p.creates != 0 {
        t.Fatal(err)
    }
    in.Plan.Requirements.MaxComputeHourlyPrice = nil
    in.Plan.Requirements.MinGPUMemoryGiB = 24
    if _, err := s.Acquire(context.Background(), in, nil); err == nil || p.creates != 0 {
        t.Fatal(err)
    }
    in.Plan.Requirements.MinGPUMemoryGiB = 0
    in.AllowUnquoted = false
    if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "BAD_CONFIG" || p.creates != 0 {
        t.Fatal(err)
    }
}

func TestFreshInventoryCannotBroadenOrIgnorePriceChange(t *testing.T) {
    p := &fakeStock{fakeProvider: &fakeProvider{}}
    s, in := serviceFixture(t, p)
    in.Plan.Product = "standard"
    in.Settings.Credential.Mode = "session"
    in.Plan.AutoDL.GPUSpecUUID = ""
    in.Plan.AutoDL.CUDAFrom = 0
    in.Plan.Requirements.MaxComputeHourlyPrice = &Money{Amount: "2.00", Currency: "CNY"}
    p.search = func(n int) (InventoryPage, error) {
        price := "1.50"
        if n == 2 {
            price = "3.00"
        }
        return InventoryPage{Offers: []Offer{{ID: "same", GPUCount: 1, Available: ptr(true), ComputeHourlyPrice: &Money{Amount: price, Currency: "CNY"}}}, Complete: true}, nil
    }
    if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "NO_MATCH" || p.creates != 0 || p.searches != 2 {
        t.Fatal(err, p.creates, p.searches)
    }
}

func TestWatchIsReadOnlyQuietAndCancellable(t *testing.T) {
    p := &fakeStock{fakeProvider: &fakeProvider{}}
    s, in := serviceFixture(t, p)
    in.Plan.WaitTimeout = "1m"
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    waits, events := 0, 0
    s.Wait = func(context.Context, time.Duration) error {
        waits++
        if waits == 3 {
            cancel()
            return ctx.Err()
        }
        return nil
    }
    _, err := s.Watch(ctx, p, in.Plan, 100, func(e Event) error {
        events++
        if e.Type != "change" {
            t.Fatal(e)
        }
        return nil
    })
    if !errors.Is(err, context.Canceled) || events != 1 || p.creates != 0 || p.searches != 3 {
        t.Fatal(err, events, p)
    }
    if _, err := os.Stat(s.Receipts.Dir); !os.IsNotExist(err) {
        t.Fatal("watch wrote local mutation state", err)
    }
}

func TestBackoffHonorsRetryAfter(t *testing.T) {
    for i := 0; i < 5; i++ {
        if d := backoff(10*time.Minute, i, nil); d < 10*time.Minute {
            t.Fatal("polled faster than configured minimum", d)
        }
    }
    for i := 0; i < 5; i++ {
        if d := backoff(10*time.Second, i, &APIError{RetryAfter: 4 * time.Minute}); d < 4*time.Minute {
            t.Fatal(d)
        }
    }
}

func TestConcurrentAcquireHasOneOwner(t *testing.T) {
    entered, release := make(chan struct{}), make(chan struct{})
    p := &fakeProvider{}
    s, in := serviceFixture(t, p)
    var creates atomic.Int32
    p.create = func() (Ref, error) {
        creates.Add(1)
        close(entered)
        <-release
        return Ref{}, &APIError{Code: "TRANSPORT", Unconfirmed: true}
    }
    done := make(chan error, 1)
    go func() { _, err := s.Acquire(context.Background(), in, nil); done <- err }()
    <-entered
    _, err := s.Acquire(context.Background(), in, nil)
    if errorCode(err) != "CLOUD_BUSY" || creates.Load() != 1 {
        t.Fatal(err, creates.Load())
    }
    close(release)
    <-done
}

func TestReceiptUnknownFormatIsPreserved(t *testing.T) {
    s, in := serviceFixture(t, &fakeProvider{})
    r, err := s.Acquire(context.Background(), in, nil)
    if err != nil {
        t.Fatal(err)
    }
    data, err := s.Receipts.store().Load(context.Background(), r.ID)
    if err != nil {
        t.Fatal(err)
    }
    var raw map[string]any
    _ = json.Unmarshal(data, &raw)
    raw["format_version"] = 999
    changed, _ := json.Marshal(raw)
    if err := s.Receipts.store().SaveEvent(context.Background(), r.ID, r.CreatedAt.Format(time.RFC3339Nano), changed, "", nil); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Receipts.Load(r.ID); err == nil {
        t.Fatal("accepted unknown receipt format")
    }

}

func TestUncertainAcquisitionDoesNotPreventStoppingExistingInstance(t *testing.T) {
    p := &fakeProvider{}
    s, in := serviceFixture(t, p)
    p.create = func() (Ref, error) { return Ref{}, &APIError{Code: "TRANSPORT", Unconfirmed: true} }
    if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "CLOUD_UNCONFIRMED" {
        t.Fatal(err)
    }
    b := Binding{Ref: Ref{Provider: "autodl", Product: "pro", Account: "demo", ID: "pro-existing"}, Settings: in.Settings}
    r, err := s.Power(context.Background(), b, false, 0)
    if err != nil || r.Status != "accepted" || r.Instance.ID != "pro-existing" {
        t.Fatal(r, err)
    }
}

func TestWatchWithoutSuccessfulObservationFails(t *testing.T) {
    p := &fakeStock{fakeProvider: &fakeProvider{}, search: func(int) (InventoryPage, error) {
        return InventoryPage{}, &APIError{Code: "TRANSPORT", Transient: true}
    }}
    s, in := serviceFixture(t, p)
    in.Plan.WaitTimeout = "1m"
    s.Wait = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
    if _, err := s.Watch(context.Background(), p, in.Plan, 100, nil); err == nil {
        t.Fatal("reported an empty successful watch without evidence")
    }
}

func TestReconcileAndPowerUseFrozenProductNotAccountDefault(t *testing.T) {
    p := &fakeProvider{create: func() (Ref, error) {
        return Ref{}, &APIError{Code: "TRANSPORT", Unconfirmed: true}
    }}
    s, in := serviceFixture(t, p)
    in.Settings.DefaultProduct = "standard"
    opened := 0
    s.Open = func(_ context.Context, _ string, a Account, product string) (Provider, error) {
        opened++
        if product != "pro" || a.DefaultProduct != "standard" || a.Credential.Mode != "developer" {
            t.Fatal("reinterpreted saved product or credential", a, product)
        }
        return p, nil
    }
    r, err := s.Acquire(context.Background(), in, nil)
    if errorCode(err) != "CLOUD_UNCONFIRMED" || r.Plan.Product != "pro" {
        t.Fatal(r, err)
    }
    reconciled, err := s.Reconcile(context.Background(), r.ID, "pro-kept", false)
    if err != nil || reconciled.Instance.Product != "pro" || reconciled.Settings.DefaultProduct != "standard" {
        t.Fatal(reconciled, err)
    }
    b := Binding{Ref: *reconciled.Instance, Settings: reconciled.Settings}
    result, err := s.Power(context.Background(), b, false, 0)
    if err != nil || result.Status != "accepted" || opened != 3 || p.creates != 1 {
        t.Fatal(result, err, opened, p.creates)
    }
    // A disagreement between the intent and a later resource observation may
    // not overwrite the valid receipt, even if it equals an account default.
    reconciled.Plan.Product = "standard"
    if err := s.Receipts.Save(reconciled); err == nil {
        t.Fatal("saved an intent/resource product mismatch")
    }
    saved, err := s.Receipts.Load(r.ID)
    if err != nil || saved.Plan.Product != "pro" {
        t.Fatal(saved, err)
    }
}

func TestAcquireRequiresASelectedProductBeforeSideEffects(t *testing.T) {
    s, in := serviceFixture(t, &fakeProvider{})
    in.Plan.Product = ""
    s.Open = func(context.Context, string, Account, string) (Provider, error) {
        t.Fatal("opened a provider before freezing the product")
        return nil, nil
    }
    if _, err := s.Acquire(context.Background(), in, nil); errorCode(err) != "BAD_CONFIG" {
        t.Fatal(err)
    }
    if _, err := os.Stat(s.Receipts.Dir); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("created state for an invalid request", err)
    }
}
