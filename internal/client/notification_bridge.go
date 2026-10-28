package client

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "sync"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/notifications"
    "github.com/elebirds/loaf/sdk/agentdelivery"
    "golang.org/x/sys/unix"
)

type bridgeUpdate struct {
    result   NotifyServeResult
    delivery *Delivery
    err      error
}

// Serve owns one Bridge lifetime. Observation, dispatch and ACK queries have
// independent bounded lanes; only this goroutine invokes the output consumer.
func (s *eventServices) Serve(ctx context.Context, in NotifyServeInput, emit func(Delivery) error) (result NotifyServeResult, err error) {
    if s.store.Path == "" || in.Interval < 0 {
        return result, asyncError("BAD_CONFIG", nil)
    }
    database, err := db.Open(ctx, s.store.Path, true)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    database.Close()
    lock, err := os.OpenFile(filepath.Join(filepath.Dir(s.store.Path), "notify-bridge.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    defer lock.Close()
    if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        return result, asyncError("NOTIFY_BUSY", err)
    }
    defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
    if err = s.store.RecoverInterrupted(ctx); err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    if in.Interval == 0 {
        in.Interval = 5 * time.Second
    }
    defer func() {
        final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
        defer cancel()
        var countErr error
        result.Pending, countErr = s.store.PendingCount(final)
        if err == nil && countErr != nil {
            err = asyncError("NOTIFY_FAILED", countErr)
        }
        result.Unconfirmed, countErr = s.store.UnconfirmedCount(final)
        if err == nil && countErr != nil {
            err = asyncError("NOTIFY_FAILED", countErr)
        }
        result.Rejected, countErr = s.store.RejectedCount(final)
        if err == nil && countErr != nil {
            err = asyncError("NOTIFY_FAILED", countErr)
        }
        if in.Once && err == nil {
            if result.Unconfirmed > 0 {
                err = asyncError("DELIVERY_UNCONFIRMED", agentdelivery.ErrUnconfirmed)
            } else if result.Rejected > 0 {
                err = asyncError("DELIVERY_REJECTED", agentdelivery.ErrUnavailable)
            }
        }
    }()
    if in.Once {
        collected, collectErr := s.collectBatch(ctx, in)
        result.Received += collected.Received
        var source sourceReadError
        if collectErr != nil && !errors.As(collectErr, &source) {
            return result, collectErr
        }
        dispatched, sendErr := s.dispatchBatch(ctx, in, emit)
        result.Attempted += dispatched.Attempted
        result.Accepted += dispatched.Accepted
        if sendErr != nil {
            return result, sendErr
        }
        looked, lookupErr := s.lookupBatch(ctx, in, emit)
        result.Accepted += looked.Accepted
        if lookupErr != nil {
            return result, lookupErr
        }
        return result, collectErr
    }
    life, cancel := context.WithCancel(ctx)
    defer cancel()
    updates := make(chan bridgeUpdate, 32)
    var workers sync.WaitGroup
    var totals NotifyServeResult
    var totalsMu sync.Mutex
    forward := func(d Delivery) error {
        select {
        case updates <- bridgeUpdate{delivery: &d}:
            return nil
        case <-life.Done():
            return life.Err()
        }
    }
    for _, lane := range []func(context.Context) (NotifyServeResult, error){
        func(c context.Context) (NotifyServeResult, error) { return s.collectBatch(c, in) },
        func(c context.Context) (NotifyServeResult, error) { return s.dispatchBatch(c, in, forward) },
        func(c context.Context) (NotifyServeResult, error) { return s.lookupBatch(c, in, forward) },
    } {
        workers.Go(func() {
            for {
                round, laneErr := lane(life)
                totalsMu.Lock()
                totals.Received += round.Received
                totals.Attempted += round.Attempted
                totals.Accepted += round.Accepted
                totalsMu.Unlock()
                select {
                case updates <- bridgeUpdate{result: round, err: laneErr}:
                case <-life.Done():
                    return
                }
                var source sourceReadError
                if laneErr != nil && !errors.As(laneErr, &source) {
                    return
                }
                timer := time.NewTimer(in.Interval)
                select {
                case <-timer.C:
                case <-life.Done():
                    timer.Stop()
                    return
                }
            }
        })
    }
    defer func() {
        cancel()
        workers.Wait()
        totalsMu.Lock()
        result.Received = totals.Received
        result.Attempted = totals.Attempted
        result.Accepted = totals.Accepted
        totalsMu.Unlock()
    }()
    for {
        select {
        case <-ctx.Done():
            return result, ctx.Err()
        case u := <-updates:
            result.Received += u.result.Received
            result.Attempted += u.result.Attempted
            result.Accepted += u.result.Accepted
            if u.delivery != nil && emit != nil {
                if emitErr := emit(*u.delivery); emitErr != nil {
                    return result, emitErr
                }
            }
            var source sourceReadError
            if u.err != nil && !errors.As(u.err, &source) {
                return result, u.err
            }
        }
    }
}

func (s *eventServices) collectBatch(ctx context.Context, in NotifyServeInput) (NotifyServeResult, error) {
    result := NotifyServeResult{}
    sources, err := s.store.DueSources(ctx, 200, time.Now().UTC(), in.Interval)
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    window, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    jobs := make(chan notifications.Source)
    var workers sync.WaitGroup
    var mu sync.Mutex
    var first error
    concurrency := 1
    if in.Policy.Batch {
        concurrency = 4
    }
    for i := 0; i < concurrency; i++ {
        workers.Go(func() {
            for src := range jobs {
                if window.Err() != nil {
                    continue
                }
                cursor, readErr := s.store.Cursor(window, src.ID)
                var page event.Page
                if readErr == nil {
                    observe, stop := context.WithTimeout(window, 20*time.Second)
                    page, readErr = s.read(observe, src, event.ReadInput{Source: src.Source, Cursor: cursor, Limit: 200, Subject: &src.Subject}, in.Policy)
                    stop()
                }
                if ctx.Err() != nil {
                    continue
                }
                var itemErr error
                received := 0
                if readErr != nil {
                    classified := asyncError("EVENT_FAILED", readErr)
                    code := "EVENT_FAILED"
                    var f *fault.Error
                    if errors.As(classified, &f) {
                        code = f.Code
                    }
                    if savedErr := s.store.RecordSourceFailure(ctx, src, code, in.Interval); savedErr != nil {
                        itemErr = asyncError("STATE_WRITE_FAILED", savedErr)
                    } else {
                        itemErr = sourceReadError{classified}
                    }
                } else {
                    received, itemErr = s.store.CollectSource(ctx, src, page)
                    if errors.Is(itemErr, notifications.ErrInactive) {
                        itemErr = nil
                    }
                    if itemErr != nil {
                        itemErr = asyncError("NOTIFY_FAILED", itemErr)
                    }
                }
                mu.Lock()
                result.Received += received
                if itemErr != nil {
                    var prior sourceReadError
                    if first == nil || errors.As(first, &prior) {
                        first = itemErr
                    }
                }
                mu.Unlock()
            }
        })
    }
feed:
    for _, src := range sources {
        select {
        case jobs <- src:
        case <-window.Done():
            break feed
        }
    }
    close(jobs)
    workers.Wait()
    return result, first
}

func (s *eventServices) dispatchBatch(ctx context.Context, in NotifyServeInput, emit func(Delivery) error) (NotifyServeResult, error) {
    result := NotifyServeResult{}
    pending, err := s.store.Pending(ctx, 200, time.Now())
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    window, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    for _, candidate := range pending {
        if window.Err() != nil {
            break
        }
        d, claimErr := s.store.Claim(window, candidate)
        if errors.Is(claimErr, notifications.ErrInactive) {
            continue
        }
        if claimErr != nil {
            return result, asyncError("NOTIFY_FAILED", claimErr)
        }
        send, stop := context.WithTimeout(window, 20*time.Second)
        receipt, sendErr := s.dependencies.Sender.Deliver(send, transportAddress(d.Destination.Address), transportMessage(d.Message))
        stop()
        result.Attempted++
        if receipt.MessageID != d.ID {
            receipt = agentdelivery.Receipt{MessageID: d.ID, State: "unconfirmed", ReasonCode: "DELIVERY_RECEIPT_INVALID"}
            sendErr = agentdelivery.ErrUnconfirmed
        }
        d.Receipt = savedEvidence(receipt)
        d.UpdatedAt = time.Now().UTC()
        d.Stage = receipt.State
        d.ErrorCode = ""
        d.NextAttemptAt = time.Time{}
        switch receipt.State {
        case "accepted", "processed":
            result.Accepted++
        case "rejected":
            d.ErrorCode = "DELIVERY_REJECTED"
            if receipt.RetrySafe {
                d.Stage = "pending"
                d.NextAttemptAt = time.Now().UTC().Add(time.Second * time.Duration(1<<min(d.Attempts, 8)))
            }
        default:
            d.Stage = "unconfirmed"
            d.NextLookupAt = d.UpdatedAt
            d.ErrorCode = "DELIVERY_UNCONFIRMED"
        }
        if sendErr != nil && d.ErrorCode == "" {
            d.ErrorCode = "DELIVERY_FAILED"
        }
        save, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
        saveErr := s.store.Finish(save, d)
        saveCancel()
        if emit != nil {
            if emitErr := emit(deliveryRecord(d)); emitErr != nil {
                return result, emitErr
            }
        }
        if saveErr != nil {
            return result, asyncError("STATE_WRITE_FAILED", saveErr)
        }
    }
    return result, nil
}

func (s *eventServices) lookupBatch(ctx context.Context, in NotifyServeInput, emit func(Delivery) error) (NotifyServeResult, error) {
    result := NotifyServeResult{}
    querier, ok := s.dependencies.Sender.(agentdelivery.ReceiptQuerier)
    if !ok {
        return result, nil
    }
    rows, err := s.store.LookupDue(ctx, 200, time.Now())
    if err != nil {
        return result, asyncError("NOTIFY_FAILED", err)
    }
    window, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    for _, d := range rows {
        if window.Err() != nil {
            break
        }
        d, err = s.store.ScheduleLookup(window, d, time.Now().UTC().Add(in.Interval))
        if err != nil {
            continue
        }
        query, stop := context.WithTimeout(window, 5*time.Second)
        receipt, queryErr := querier.Lookup(query, transportAddress(d.Destination.Address), transportEvidence(d.Receipt))
        stop()
        if queryErr != nil || receipt.MessageID != d.ID || !receipt.Accepted() {
            continue
        }
        d.Receipt = savedEvidence(receipt)
        d.Stage = receipt.State
        d.ErrorCode = ""
        d.UpdatedAt = time.Now().UTC()
        if err = s.store.Reconcile(ctx, d); err != nil {
            current, getErr := s.store.Delivery(ctx, d.ID)
            if getErr == nil && current.Stage != "unconfirmed" {
                continue
            }
            return result, asyncError("STATE_WRITE_FAILED", err)
        }
        result.Accepted++
        if emit != nil {
            if err = emit(deliveryRecord(d)); err != nil {
                return result, err
            }
        }
    }
    return result, nil
}
