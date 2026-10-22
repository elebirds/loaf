package cloud

import (
    "context"
    "errors"
    "math/rand/v2"
    "time"

    "github.com/elebirds/loaf/internal/fault"
)

// Event is human progress or a machine-readable monitoring change.
type Event struct {
    Type        string        `json:"type"`
    At          time.Time     `json:"at"`
    Observation *SearchResult `json:"observation,omitempty"`
    RequestID   string        `json:"request_id,omitempty"`
    Instance    *Ref          `json:"instance,omitempty"`
    NextCheckAt *time.Time    `json:"next_check_at,omitempty"`
}

// Service owns bounded policy and write-ahead intent, not HTTP or SSH execution.
type Service struct {
    Open     OpenProvider
    Receipts Receipts
    // Wait is injectable for deterministic backoff/cancellation tests.
    Wait func(context.Context, time.Duration) error
}

func (s *Service) pause(ctx context.Context, d time.Duration) error {
    if s.Wait != nil {
        return s.Wait(ctx, d)
    }
    timer := time.NewTimer(d)
    defer timer.Stop()
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
        return nil
    }
}

func emitEvent(emit func(Event) error, e Event) error {
    if emit == nil {
        return nil
    }
    e.At = time.Now().UTC()
    return emit(e)
}

// Search performs one read-only bounded observation.
func (s *Service) Search(ctx context.Context, p Provider, plan Plan, limit int) (SearchResult, error) {
    inventory, ok := p.(Inventory)
    if !ok {
        return SearchResult{Plan: plan}, &APIError{Code: "UNSUPPORTED_CAPABILITY"}
    }
    page, err := inventory.Search(ctx, plan, limit)
    return Select(page, plan), err
}

// Watch is read-only, foreground and deadline bounded. Unchanged inventories
// produce at most a minute heartbeat, not a stream of full duplicate catalogs.
func (s *Service) Watch(ctx context.Context, p Provider, plan Plan, limit int, emit func(Event) error) (SearchResult, error) {
    wait, _ := time.ParseDuration(plan.WaitTimeout)
    if wait <= 0 {
        return SearchResult{}, invalid()
    }
    if _, ok := p.(Inventory); !ok {
        return SearchResult{}, &APIError{Code: "UNSUPPORTED_CAPABILITY"}
    }
    watch, cancel := context.WithTimeout(ctx, wait)
    defer cancel()
    interval, _ := time.ParseDuration(plan.PollInterval)
    var last SearchResult
    signature := ""
    heartbeat := time.Time{}
    failures := 0
    succeeded := false
    for {
        observed, err := s.Search(watch, p, plan, limit)
        delay := backoff(interval, 0, nil)
        if err == nil {
            succeeded = true
            last = observed
            failures = 0
            key := observationKey(observed)
            kind := "change"
            changed := key != signature
            if !changed {
                kind = "heartbeat"
            }
            if changed || time.Since(heartbeat) >= time.Minute {
                next := time.Now().Add(delay).UTC()
                e := Event{Type: kind, NextCheckAt: &next}
                if changed {
                    e.Observation = &observed
                }
                if err := emitEvent(emit, e); err != nil {
                    return last, err
                }
                heartbeat = time.Now()
                signature = key
            }
        } else {
            var api *APIError
            if !errors.As(err, &api) || !api.Transient {
                return last, err
            }
            failures++
            if failures >= 5 {
                return last, err
            }
            delay = backoff(interval, failures, err)
        }
        if err := s.pause(watch, delay); err != nil {
            if ctx.Err() != nil {
                return last, ctx.Err()
            }
            if !succeeded {
                return last, fault.New("CLOUD_FAILED", "cloud_no_observation", nil)
            }
            return last, nil // The read-only observation window ended normally.
        }
    }
}

// AcquireInput is one explicitly authorized request for at most one resource.
type AcquireInput struct {
    Account            string
    Settings           Account
    Target, RemoteBase string
    Plan               Plan
    Limit              int
    AllowUnquoted      bool
    CheckTarget        func() error
}

// Acquire preserves the request ID on all outcomes. A lost create response is
// never retried; later invocations require explicit receipt reconciliation.
func (s *Service) Acquire(ctx context.Context, in AcquireInput, emit func(Event) error) (Receipt, error) {
    if !ValidProduct(in.Plan.Product) {
        return Receipt{}, invalid()
    }
    if in.Plan.Clone != nil {
        if err := validateClone(in.Plan, in.Account); err != nil {
            return Receipt{}, err
        }
        if in.Settings.Credential.Mode != "session" {
            return Receipt{}, Failure(&APIError{Code: "UNSUPPORTED_CAPABILITY"})
        }
    }
    var result Receipt
    err := s.Receipts.Lock(ctx, "target-"+in.Target, func() error {
        return s.Receipts.Lock(ctx, "allocate-"+in.Account, func() error {
            if in.CheckTarget != nil {
                if err := in.CheckTarget(); err != nil {
                    return err
                }
            }
            if err := s.Receipts.pending(in.Account, ""); err != nil {
                return err
            }
            rows, err := s.Receipts.List()
            if err != nil {
                return err
            }
            for _, r := range rows {
                if r.Operation == "acquire" && r.Target == in.Target && r.Status == "created" {
                    return fault.New("CLOUD_UNCONFIRMED", "cloud_pending", map[string]any{"ID": r.ID})
                }
            }
            p, err := s.Open(ctx, in.Account, in.Settings, in.Plan.Product)
            if err != nil {
                return err
            }
            inventory, hasInventory := p.(Inventory)
            clone, canClone := p.(Cloner)
            if in.Plan.Clone != nil && (!canClone || !hasInventory) {
                return fault.New("UNSUPPORTED_CAPABILITY", "cloud_clone_unsupported", nil)
            }
            r := in.Plan.Requirements
            if !hasInventory {
                if r.MaxComputeHourlyPrice != nil {
                    return fault.New("PRICE_UNVERIFIABLE", "cloud_price_unverifiable", nil)
                }
                if !in.AllowUnquoted {
                    return fault.New("BAD_CONFIG", "cloud_unquoted_required", nil)
                }
                // A concrete Pro spec is not evidence for unrelated hardware
                // floors. Reject conditions this API cannot actually verify.
                if r.GPUVendor != "" || len(r.GPUModels) > 0 || r.MinGPUMemoryGiB > 0 || r.MinCPUs > 0 || r.MinMemoryGiB > 0 || r.MinDiskGiB > 0 {
                    return &APIError{Code: "UNSUPPORTED_CAPABILITY"}
                }
            }
            if in.Plan.Clone == nil {
                if err := validateCreate(in.Plan, in.Settings.Credential.Mode); err != nil {
                    return err
                }
            }
            wait, _ := time.ParseDuration(in.Plan.WaitTimeout)
            interval, _ := time.ParseDuration(in.Plan.PollInterval)
            now := time.Now().UTC()
            result, err = s.Receipts.New(Receipt{Operation: "acquire", Account: in.Account, Settings: in.Settings, Target: in.Target, RemoteBase: in.RemoteBase, Plan: in.Plan, AllowUnquoted: in.AllowUnquoted, CreatedAt: now, Deadline: now.Add(wait), Status: "not_created"})
            if err != nil {
                return err
            }
            if err := s.Receipts.Save(result); err != nil {
                return err
            }
            if !hasInventory {
                if err := emitEvent(emit, Event{Type: "allocation_attempt", RequestID: result.ID}); err != nil {
                    return err
                }
            }
            // Even a zero monitoring window permits one bounded API attempt.
            window := ctx
            cancel := func() {}
            if wait > 0 {
                window, cancel = context.WithDeadline(ctx, result.Deadline)
            }
            defer cancel()
            failures := 0
            progressKey := ""
            lastProgress := time.Time{}
            for {
                if err := window.Err(); err != nil {
                    return fault.Wrap("NO_MATCH", "cloud_no_match", err)
                }
                var selected *Offer
                var readErr error
                if hasInventory {
                    page, err := inventory.Search(window, in.Plan, in.Limit)
                    readErr = err
                    if err == nil {
                        found := Select(page, in.Plan)
                        if len(found.Matches) > 0 {
                            // Inventory is not a reservation. A second fresh
                            // query must still contain the same eligible offer.
                            id := found.Matches[0].ID
                            fresh, e := inventory.Search(window, in.Plan, in.Limit)
                            readErr = e
                            if e == nil {
                                found = Select(fresh, in.Plan)
                                for _, offer := range found.Matches {
                                    if offer.ID == id {
                                        selected = &offer
                                        break
                                    }
                                }
                            }
                        }
                        key := observationKey(found)
                        if key != progressKey || time.Since(lastProgress) >= time.Minute {
                            if err := emitEvent(emit, Event{Type: "waiting", Observation: &found, RequestID: result.ID}); err != nil {
                                return err
                            }
                            progressKey, lastProgress = key, time.Now()
                        }
                    }
                }
                if readErr != nil {
                    var api *APIError
                    if !errors.As(readErr, &api) || !api.Transient {
                        return readErr
                    }
                    failures++
                    if failures >= 5 {
                        return readErr
                    }
                } else {
                    failures = 0
                }
                if readErr == nil && (!hasInventory || selected != nil) {
                    if selected != nil && selected.ComputeHourlyPrice == nil && !in.AllowUnquoted {
                        return fault.New("PRICE_UNVERIFIABLE", "cloud_price_unverifiable", nil)
                    }
                    if err := window.Err(); err != nil {
                        return err
                    }
                    result.Attempts++
                    result.Status = "intent"
                    result.Offer = selected
                    if err := s.Receipts.Save(result); err != nil {
                        return err
                    }
                    if err := emitEvent(emit, Event{Type: "creating", RequestID: result.ID}); err != nil {
                        result.Status = "not_created"
                        _ = s.Receipts.Save(result)
                        return err
                    }
                    var ref Ref
                    var err error
                    if in.Plan.Clone != nil {
                        ref, err = clone.Clone(window, in.Plan, selected)
                    } else {
                        ref, err = p.Create(window, in.Plan, selected)
                    }
                    if err == nil {
                        result.Instance = &ref
                        result.Status = "created"
                        if saveErr := s.Receipts.Save(result); saveErr != nil {
                            return fault.Wrap("CLOUD_UNCONFIRMED", "cloud_unconfirmed", saveErr)
                        }
                        return emitEvent(emit, Event{Type: "created", RequestID: result.ID, Instance: &ref})
                    }
                    var api *APIError
                    if errors.As(err, &api) && !api.Unconfirmed {
                        result.Status = "rejected"
                        if api.NoStock {
                            result.Status = "no_stock"
                        }
                        if saveErr := s.Receipts.Save(result); saveErr != nil {
                            return saveErr
                        }
                        if !api.NoStock {
                            return err
                        }
                        readErr = err // Honor Retry-After on a confirmed no-stock response.
                    } else {
                        result.Status = "uncertain"
                        _ = s.Receipts.Save(result) // The durable intent already blocks repeats.
                        return fault.Wrap("CLOUD_UNCONFIRMED", "cloud_unconfirmed", err)
                    }
                }
                if wait == 0 {
                    return fault.New("NO_MATCH", "cloud_no_match", nil)
                }
                if err := s.pause(window, backoff(interval, failures, readErr)); err != nil {
                    return fault.Wrap("NO_MATCH", "cloud_no_match", err)
                }
            }
        })
    })
    return result, Failure(err)
}

// MarkReady records completed binding without changing any creation intent.
func (s *Service) MarkReady(ctx context.Context, id string, ref Ref) error {
    return s.Receipts.Lock(ctx, "receipt-"+id, func() error {
        r, err := s.Receipts.Load(id)
        if err != nil {
            return err
        }
        if r.Instance == nil || *r.Instance != ref || r.Operation != "acquire" || r.Status != "created" && r.Status != "ready" {
            return invalid()
        }
        r.Status = "ready"
        return s.Receipts.Save(r)
    })
}

func validateCreate(p Plan, mode string) error {
    for _, id := range []string{p.AutoDL.Image, p.AutoDL.PrivateImageUUID, p.AutoDL.GPUSpecUUID} {
        if id != "" && !platformID.MatchString(id) {
            return invalid()
        }
    }
    if p.Product == "pro" && mode == "developer" && (p.AutoDL.GPUSpecID != 0 || p.AutoDL.Image == "" || p.AutoDL.GPUSpecUUID == "" || p.AutoDL.CUDAFrom <= 0 || p.Requirements.GPUCount > 4 || p.AutoDL.ExpandDataDiskGB != 0 || p.AutoDL.PrivateImageUUID != "") {
        return invalid()
    }
    if p.Product == "pro" && mode == "session" && ((p.AutoDL.Image == "") == (p.AutoDL.PrivateImageUUID == "") || p.AutoDL.GPUSpecUUID != "" || p.AutoDL.CUDAFrom <= 0 || p.Requirements.GPUCount > 8 || p.AutoDL.ExpandDataDiskGB != 0) {
        return invalid()
    }
    if p.Product == "standard" && (p.AutoDL.GPUSpecID != 0 || (p.AutoDL.Image == "") == (p.AutoDL.PrivateImageUUID == "") || p.AutoDL.ExpandSystemDiskGB != 0 || p.AutoDL.GPUSpecUUID != "" || p.AutoDL.CUDAFrom != 0) {
        return invalid()
    }
    return nil
}

// Power submits once and observes the same resource. Accepted is not completed.
func (s *Service) Power(ctx context.Context, b Binding, on bool, wait time.Duration) (Receipt, error) {
    var result Receipt
    err := s.Receipts.Lock(ctx, "power-"+b.Ref.Account+"-"+b.Ref.ID, func() error {
        if err := s.Receipts.pending(b.Ref.Account, b.Ref.ID); err != nil {
            return err
        }
        p, err := s.Open(ctx, b.Ref.Account, b.Settings, b.Ref.Product)
        if err != nil {
            return err
        }
        operation, want := "stop", "stopped"
        if on {
            operation, want = "start", "running"
        }
        current, err := p.Get(ctx, b.Ref)
        if err != nil {
            return err
        }
        if current.Status == want {
            result = Receipt{Status: "completed", Instance: &b.Ref}
            return nil
        }
        now := time.Now().UTC()
        result, err = s.Receipts.New(Receipt{Operation: operation, Account: b.Ref.Account, Settings: b.Settings, CreatedAt: now, Deadline: now.Add(wait), Status: "intent", Attempts: 1, Instance: &b.Ref})
        if err != nil {
            return err
        }
        if err := s.Receipts.Save(result); err != nil {
            return err
        }
        if on {
            err = p.Start(ctx, b.Ref)
        } else {
            err = p.Stop(ctx, b.Ref)
        }
        if err != nil {
            result.Status = "uncertain"
            var api *APIError
            if errors.As(err, &api) && !api.Unconfirmed {
                result.Status = "rejected"
            }
            _ = s.Receipts.Save(result)
            if result.Status == "uncertain" {
                return fault.Wrap("CLOUD_UNCONFIRMED", "cloud_unconfirmed", err)
            }
            return err
        }
        result.Status = "accepted"
        if err := s.Receipts.Save(result); err != nil {
            return fault.Wrap("CLOUD_UNCONFIRMED", "cloud_unconfirmed", err)
        }
        if wait == 0 {
            return nil
        }
        window, cancel := context.WithDeadline(ctx, result.Deadline)
        defer cancel()
        for {
            current, err := p.Get(window, b.Ref)
            if err != nil {
                return fault.Wrap("CLOUD_UNCONFIRMED", "cloud_unconfirmed", err)
            }
            if current.Status == want {
                result.Status = "completed"
                return s.Receipts.Save(result)
            }
            if err := s.pause(window, 10*time.Second); err != nil {
                return fault.Wrap("CLOUD_UNCONFIRMED", "cloud_unconfirmed", err)
            }
        }
    })
    return result, Failure(err)
}

// Reconcile never creates/starts/stops resources. A human may bind an uncertain
// allocation to its known ID, or explicitly attest that no instance was made.
func (s *Service) Reconcile(ctx context.Context, id, instance string, noInstance bool) (Receipt, error) {
    r, err := s.Receipts.Load(id)
    if err != nil {
        return r, err
    }
    scope := "allocate-" + r.Account
    if r.Operation != "acquire" && r.Instance != nil {
        scope = "power-" + r.Account + "-" + r.Instance.ID
    }
    err = s.Receipts.Lock(ctx, scope, func() error {
        var err error
        r, err = s.Receipts.Load(id)
        if err != nil {
            return err
        }
        if r.Status != "intent" && r.Status != "uncertain" && r.Status != "accepted" {
            return invalid()
        }
        if noInstance {
            if r.Operation != "acquire" || r.Instance != nil || instance != "" {
                return invalid()
            }
            r.Status = "not_created"
        } else {
            if r.Operation != "acquire" && instance != "" && (r.Instance == nil || instance != r.Instance.ID) {
                return invalid()
            }
            ref := r.Instance
            if r.Operation == "acquire" && instance != "" {
                ref = &Ref{Provider: r.Settings.Provider, Product: r.Plan.Product, Account: r.Account, ID: instance}
            }
            if ref == nil {
                return invalid()
            }
            p, err := s.Open(ctx, r.Account, r.Settings, ref.Product)
            if err != nil {
                return err
            }
            observed, err := p.Get(ctx, *ref)
            if err != nil {
                return err
            }
            if source := r.Plan.Clone; source != nil && (*ref == source.Instance || observed.Region != source.Region) {
                return fault.New("BAD_CONFIG", "cloud_clone_region", nil)
            }
            r.Instance = ref
            if r.Operation == "acquire" {
                r.Status = "created"
            } else {
                want := "stopped"
                if r.Operation == "start" {
                    want = "running"
                }
                if observed.Status != want {
                    return fault.New("CLOUD_UNCONFIRMED", "cloud_unconfirmed", nil)
                }
                r.Status = "completed"
            }
        }
        return s.Receipts.Save(r)
    })
    return r, Failure(err)
}

func backoff(base time.Duration, failures int, err error) time.Duration {
    d := min(base*time.Duration(1<<min(failures, 5)), max(base, 5*time.Minute))
    // Positive jitter never polls faster than the user's interval.
    d += time.Duration(rand.Int64N(max(1, int64(d/5))))
    var api *APIError
    if errors.As(err, &api) {
        d = max(d, api.RetryAfter)
    }
    return d
}
