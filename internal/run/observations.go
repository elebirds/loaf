package run

import (
    "context"
    "errors"
    "time"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/task"
)

// ObservationStore stores public summaries independently of frozen locators.
type ObservationStore interface {
    RunObservation(string, string) (task.RunObservation, error)
    SaveRunObservation(context.Context, string, string, task.RunObservation) error
}

func (s Service) localView(ref task.RunRef) (View, error) {
    v := view(ref, nil)
    if s.Observations != nil {
        observation, err := s.Observations.RunObservation(ref.ProjectID, ref.ID)
        if err != nil {
            return v, err
        }
        v.LastObservation, v.LastQueryFailure = observation.Last, observation.Failure
    }
    return v, nil
}

// remember receives only a query failure. Consumption and local persistence
// errors do not invalidate an independently verified remote observation.
func (s Service) remember(ctx context.Context, ref task.RunRef, v View, queryErr error) error {
    if s.Observations == nil || v.ID == "" || ref.Cleanup != nil || ref.Abandonment != nil && ref.Abandonment.NoStartAt != nil {
        return nil
    }
    next := task.RunObservation{}
    now := time.Now().UTC()
    // A final failure can coexist with previously confirmed stream events.
    if !v.ObservedAt.IsZero() {
        next.Last = &task.ObservedRun{ObservedAt: v.ObservedAt, Status: v.Status, Phase: v.Phase, ExitCode: v.ExitCode, Signal: v.Signal, CompletedAt: v.CompletedAt}
    }
    // Local cancellation/observation-budget expiry does not establish a new
    // connection failure. Keep the last actual failure independently.
    if queryErr != nil && ctx.Err() == nil {
        stage := fault.Stage(queryErr)
        if stage == "" {
            stage = "observe"
        }
        next.Failure = &task.QueryFailure{ObservedAt: now, Code: errorCode(queryErr), Stage: stage}
    }
    if next.Last == nil && next.Failure == nil {
        return nil
    }
    saved, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
    defer cancel()
    return s.Observations.SaveRunObservation(saved, ref.ProjectID, ref.ID, next)
}

func recordStateError(v *View, err error) error {
    if err == nil {
        return nil
    }
    var known *fault.Error
    if !errors.As(err, &known) {
        err = fault.Wrap("STATE_FAILED", "run_state", err)
    }
    v.StateError = errorCode(err)
    return fault.At(err, "local_state")
}
