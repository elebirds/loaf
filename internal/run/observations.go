package run

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// ObservationStore stores public summaries independently of frozen locators.
type ObservationStore interface {
    RunObservation(string, string) (config.RunObservation, error)
    SaveRunObservation(context.Context, string, string, config.RunObservation) error
}

func (s Service) localView(ref config.RunRef) (View, error) {
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

func (s Service) remember(ctx context.Context, ref config.RunRef, v View, err error) error {
    if s.Observations == nil || v.ID == "" || ref.Cleanup != nil {
        return nil
    }
    next := config.RunObservation{}
    now := time.Now().UTC()
    // A final failure can coexist with previously confirmed stream events.
    if !v.ObservedAt.IsZero() {
        next.Last = &config.ObservedRun{ObservedAt: v.ObservedAt, Status: v.Status, Phase: v.Phase, ExitCode: v.ExitCode, Signal: v.Signal, CompletedAt: v.CompletedAt}
    }
    // Local cancellation/observation-budget expiry does not establish a new
    // connection failure. Keep the last actual failure independently.
    if err != nil && ctx.Err() == nil {
        stage := fault.Stage(err)
        if stage == "" {
            stage = "observe"
        }
        next.Failure = &config.QueryFailure{ObservedAt: now, Code: errorCode(err), Stage: stage}
    }
    if next.Last == nil && next.Failure == nil {
        return nil
    }
    saved, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
    defer cancel()
    return s.Observations.SaveRunObservation(saved, ref.ProjectID, ref.ID, next)
}
