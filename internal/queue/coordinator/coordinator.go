// Package coordinator combines admission policy with single-attempt execution.
// It does not supervise processes or interpret workload semantics.
package coordinator

import (
    "context"

    "github.com/elebirds/loaf/internal/queue"
    queuestate "github.com/elebirds/loaf/internal/state/queue"
)

// Outcome contains only verified execution facts for the fixed Run.
type Outcome struct {
    Accepted bool
    Terminal bool
    Code     string
}

// Runs is owned by this dispatch consumer; an adapter reuses the Run supervisor.
type Runs interface {
    Launch(context.Context, queuestate.Entry) (Outcome, error)
    Observe(context.Context, queuestate.Entry) (Outcome, error)
}

// Coordinator has one OS lifetime owner. Its transactions never span Runs calls.
type Coordinator struct {
    Store  *queuestate.Store
    Policy queue.Policy
    Runs   Runs
}

// Tick reconciles existing reservations before admitting new work.
// A persisted attempted launch is observed only, never replayed.
func (c Coordinator) Tick(ctx context.Context) error {
    active, err := c.Store.Active(ctx)
    if err != nil {
        return err
    }
    for _, e := range active {
        if err = ctx.Err(); err != nil {
            return err
        }
        if !e.Attempted {
            if err = c.launch(ctx, e); err != nil {
                return err
            }
            continue
        }
        outcome, observeErr := c.Runs.Observe(ctx, e)
        if outcome.Accepted {
            _, err = c.Store.Confirm(ctx, e.Request.ID, outcome.Terminal)
        } else {
            code := outcome.Code
            if code == "" {
                code = "QUEUE_EXECUTION_UNCONFIRMED"
            }
            _, err = c.Store.Unconfirmed(ctx, e.Request.ID, code)
        }
        if err != nil {
            return err
        }
        // Observation failure retains the slot; unrelated available slots may
        // still dispatch. Transport errors are not process-stop evidence.
        _ = observeErr
    }
    if err = ctx.Err(); err != nil {
        return err
    }
    policy := c.Policy
    if policy == nil {
        policy = queue.FIFO{}
    }
    selected, err := c.Store.Reserve(ctx, policy)
    if err != nil {
        return err
    }
    for _, e := range selected {
        if err = ctx.Err(); err != nil {
            return err
        }
        if err = c.launch(ctx, e); err != nil {
            return err
        }
    }
    return nil
}

func (c Coordinator) launch(ctx context.Context, e queuestate.Entry) error {
    if err := ctx.Err(); err != nil {
        return err
    }
    attempted, err := c.Store.MarkAttempted(ctx, e.Request.ID)
    if err != nil {
        return err
    }
    outcome, launchErr := c.Runs.Launch(ctx, attempted)
    if outcome.Accepted {
        _, err = c.Store.Confirm(ctx, e.Request.ID, outcome.Terminal)
    } else {
        code := outcome.Code
        if code == "" {
            code = "QUEUE_DISPATCH_UNCONFIRMED"
        }
        _, err = c.Store.Unconfirmed(ctx, e.Request.ID, code)
    }
    _ = launchErr
    return err
}
