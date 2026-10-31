package run

import (
    "context"
    "errors"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

// Stop sends one stop request to the bound runner, then observes that same run.
// Neither lost confirmation nor cancellation replays or withdraws the request.
func (s Service) Stop(ctx context.Context, ref task.RunRef, grace *time.Duration, batch bool) (View, error) {
    result := view(ref, nil)
    if err := AvailabilityError(ref); err != nil {
        return result, err
    }
    prepared, prepareErr := s.prepareRef(ctx, ref, batch)
    if prepareErr != nil {
        return result, prepareErr
    }
    req := request(ref, 0, 0)
    req.Action = "stop"
    if grace != nil {
        if *grace < 0 || *grace > wire.MaxStopGrace {
            return result, fault.New("BAD_CONFIG", "invalid_stop_grace", nil)
        }
        millis := int64((*grace + time.Millisecond - 1) / time.Millisecond)
        req.GraceMillis = &millis
    }
    submitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
    record, err := s.Remote.Control(submitCtx, ref.Connection, prepared.Runner, req, batch)
    cancel()
    if record != nil && matching(ref, record) {
        result = view(ref, record)
        if wire.Terminal(result.Status) {
            return result, nil
        }
    } else if record != nil {
        return result, unconfirmedStop(fault.New("BAD_RESPONSE", "run_query", nil))
    }
    if err != nil {
        switch errorCode(err) {
        case "BAD_REQUEST", "BAD_FORMAT", "VERSION_MISMATCH", "UNSAFE_PATH", "PROJECT_MISMATCH", "RUN_NOT_FOUND":
            return result, remoteFault(err)
        case "STOP_UNCONFIRMED":
            return result, unconfirmedStop(err)
        }
        result.QueryError = errorCode(err)
    }
    if ctx.Err() != nil {
        return result, unconfirmedStop(ctx.Err())
    }
    budget := wire.DefaultStopGrace
    if grace != nil {
        budget = *grace
    }
    // A repeated request must wait on the first accepted policy, not its override.
    if result.StopGraceMillis != nil {
        if *result.StopGraceMillis < 0 || *result.StopGraceMillis > wire.MaxStopGrace.Milliseconds() {
            return result, unconfirmedStop(fault.New("BAD_RESPONSE", "run_query", nil))
        }
        budget = time.Duration(*result.StopGraceMillis) * time.Millisecond
    }
    budget = min(budget+5*time.Second, 5*time.Minute)
    observer := s
    // Reuse the verified control helper without making it the saved Run's
    // execution binding when an uncertain submission becomes confirmed.
    observer.Runner = func(context.Context, config.Target, string, bool) (string, error) { return prepared.Runner, nil }
    observed, observeErr := observer.Observe(ctx, ref, budget, 0, batch)
    observed.Log = nil
    if observed.StopGraceMillis == nil {
        observed.StopGraceMillis = result.StopGraceMillis
    }
    if !observed.ObservedAt.IsZero() && observed.QueryError == "" && wire.Terminal(observed.Status) {
        return observed, observeErr
    }
    if observeErr != nil {
        if observed.StateError != "" && observed.QueryError == "" {
            return observed, unconfirmedStop(observeErr)
        }
        if observed.Status == "unknown" && observed.Reason == "" {
            result.QueryError = observed.QueryError
            result.StateError = observed.StateError
            return result, unconfirmedStop(observeErr)
        }
    }
    return observed, unconfirmedStop(observeErr)
}

func unconfirmedStop(cause error) error {
    f := fault.New("STOP_UNCONFIRMED", "stop_unconfirmed", nil)
    f.Cause = cause
    f.Hint = "stop_query_hint"
    var original *fault.Error
    if errors.As(cause, &original) {
        if detail, ok := original.Params["Detail"].(string); ok {
            f.Params = map[string]any{"Detail": detail}
        }
    }
    return f
}
