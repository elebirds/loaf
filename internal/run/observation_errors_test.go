package run

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type failingObservations struct{ ObservationStore }

func (failingObservations) SaveRunObservation(context.Context, string, string, task.RunObservation) error {
    return fault.New("BAD_CONFIG", "run_state", nil)
}

func TestObserveKeepsConfirmedFactsWhenLocalStateCannotBeSaved(t *testing.T) {
    for _, failedSave := range []string{"submission", "observation"} {
        t.Run(failedSave, func(t *testing.T) {
            service, input, _, remote := executionFixture(t)
            store := service.Index.(*local.Store)
            if failedSave == "submission" {
                remote.lost, remote.unknown = true, true
            }
            submitted, err := service.Submit(context.Background(), input, nil)
            if (err != nil) != (failedSave == "submission") {
                t.Fatal(submitted, err)
            }
            prior := &task.QueryFailure{ObservedAt: time.Now().UTC().Add(-time.Minute), Code: "SSH_FAILED", Stage: "observe"}
            if err := store.SaveRunObservation(context.Background(), input.Project.ID, submitted.ID, task.RunObservation{Failure: prior}); err != nil {
                t.Fatal(err)
            }
            service.Remote = confirmedObservation{status: "failed"}
            service.Observations = store
            if failedSave == "submission" {
                service.Index = &failIndex{RunIndex: store, failAt: 1}
            } else {
                service.Observations = failingObservations{store}
            }
            observed, err := service.Observe(context.Background(), submitted.Ref, 0, 100, true)
            if err == nil || observed.Submission != "submitted" || observed.Status != "failed" || observed.StateError != "BAD_CONFIG" || observed.QueryError != "" || observed.ObservedAt.IsZero() || observed.Log == nil || fault.Stage(err) != "local_state" {
                t.Fatal("local save failure changed the remote fact", observed, err)
            }
            saved, err := store.RunObservation(input.Project.ID, submitted.ID)
            if err != nil || saved.Failure == nil || !saved.Failure.ObservedAt.Equal(prior.ObservedAt) || saved.Failure.Code != prior.Code {
                t.Fatal("local save failure became a query failure", saved, err)
            }
            if failedSave == "submission" {
                ref, err := store.Run(input.Project.ID, submitted.ID)
                if err != nil || ref.Submission != "uncertain" || saved.Last == nil || saved.Last.Status != "failed" {
                    t.Fatal(ref, saved, err)
                }
            }
        })
    }
}

type factStreamRemote struct {
    Remote
    calls int
}

func (r *factStreamRemote) Follow(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool, emit func(wire.LogEvent) error) error {
    r.calls++
    for i, status := range []string{"running", "succeeded"} {
        kind := "chunk"
        if i == 1 {
            kind = "end"
        }
        offset := int64((i + 1) * 4)
        record := &wire.RunRecord{FormatVersion: wire.FormatVersion, Request: req, Status: status, Phase: "command", SupervisorPID: 123}
        if err := emit(wire.LogEvent{Type: kind, Record: record, Log: wire.LogChunk{Text: "log\n", Offset: offset, Cursor: testCursor(req, offset)}}); err != nil {
            return err
        }
    }
    return nil
}

func TestFollowSeparatesExecutionFactsFromConsumedLogCursor(t *testing.T) {
    service, input, _, _ := executionFixture(t)
    submitted, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    store := service.Index.(*local.Store)
    prior := &task.QueryFailure{ObservedAt: time.Now().UTC().Add(-time.Minute), Code: "SSH_FAILED", Stage: "observe"}
    if err := store.SaveRunObservation(context.Background(), input.Project.ID, submitted.ID, task.RunObservation{Failure: prior}); err != nil {
        t.Fatal(err)
    }
    service.Observations = store
    remote := &factStreamRemote{}
    service.Remote = remote
    sinkError := errors.New("consumer could not commit the second chunk")
    consumed := 0
    observed, err := service.FollowResumable(context.Background(), submitted.Ref, 0, 100, true, "", func(v View, kind string) error {
        consumed++
        if consumed == 2 {
            return sinkError
        }
        return nil
    })
    if !errors.Is(err, sinkError) || errorCode(err) != "LOG_CONSUMER_FAILED" || remote.calls != 1 || observed.Status != "succeeded" || observed.ObservedAt.IsZero() || observed.QueryError != "" || observed.Log == nil || observed.Log.Offset != 4 {
        t.Fatal(observed, err, remote.calls)
    }
    saved, err := store.RunObservation(input.Project.ID, submitted.ID)
    if err != nil || saved.Last == nil || saved.Last.Status != "succeeded" || saved.Failure == nil || !saved.Failure.ObservedAt.Equal(prior.ObservedAt) {
        t.Fatal("consumer failure changed query history", saved, err)
    }
}

func TestFollowConfirmationSaveFailureDoesNotConsumeOrReconnect(t *testing.T) {
    service, input, _, remote := executionFixture(t)
    remote.lost, remote.unknown = true, true
    submitted, err := service.Submit(context.Background(), input, nil)
    if err == nil || submitted.Submission != "uncertain" {
        t.Fatal(submitted, err)
    }
    store := service.Index.(*local.Store)
    service.Index = &failIndex{RunIndex: store, failAt: 1}
    service.Observations = store
    stream := &factStreamRemote{}
    service.Remote = stream
    consumed := 0
    observed, err := service.FollowResumable(context.Background(), submitted.Ref, 0, 100, true, "", func(View, string) error {
        consumed++
        return nil
    })
    if err == nil || stream.calls != 1 || consumed != 0 || observed.Submission != "submitted" || observed.Status != "running" || observed.ObservedAt.IsZero() || observed.Log != nil || observed.StateError != "BAD_CONFIG" || observed.QueryError != "" {
        t.Fatal(observed, err, stream.calls, consumed)
    }
    saved, err := store.RunObservation(input.Project.ID, submitted.ID)
    if err != nil || saved.Last == nil || saved.Last.Status != "running" || saved.Failure != nil {
        t.Fatal(saved, err)
    }
}

func TestBatchObservationSeparatesLocalStateFailures(t *testing.T) {
    refs := historyRefs()[:2]
    remote := &historyRemote{status: map[string]string{"run-0": "failed", "run-1": "succeeded"}}
    service := Service{Index: historyIndex{refs}, Remote: remote, Observations: failingObservations{}}
    history, err := service.History(context.Background(), "project", HistoryFilters{Statuses: []string{"succeeded"}}, 2, true)
    if errorCode(err) != "STATE_PARTIAL" || history.Unresolved != 0 || history.StateErrors != 2 || history.Matched != 1 || len(history.Runs) != 2 {
        t.Fatal(history, err)
    }
    waited, err := service.Wait(context.Background(), refs, time.Second, true, nil)
    if errorCode(err) != "STATE_PARTIAL" || waited.Completed != 2 || waited.Pending != 0 || waited.QueryErrors != 0 || waited.StateErrors != 2 {
        t.Fatal(waited, err)
    }
    for _, v := range waited.Runs {
        if v.QueryError != "" || v.StateError != "BAD_CONFIG" || v.ObservedAt.IsZero() {
            t.Fatal(v)
        }
    }
}

func TestFollowPreparationFailureKeepsQueryErrorWhenRememberAlsoFails(t *testing.T) {
    for _, failSave := range []bool{false, true} {
        service, input, _, _ := executionFixture(t)
        submitted, err := service.Submit(context.Background(), input, nil)
        if err != nil {
            t.Fatal(err)
        }
        store := service.Index.(*local.Store)
        service.Observations = store
        if failSave {
            service.Observations = failingObservations{store}
        }
        service.Runner = func(context.Context, config.Target, string, bool) (string, error) {
            return "", fault.At(fault.New("SSH_FAILED", "ssh_failed", nil), "runner_verify")
        }
        stream := &factStreamRemote{}
        service.Remote = stream
        observed, err := service.Follow(context.Background(), submitted.Ref, 0, 100, true, nil)
        if errorCode(err) != "SSH_FAILED" || observed.ID != submitted.ID || observed.QueryError != "SSH_FAILED" || (observed.StateError == "BAD_CONFIG") != failSave || stream.calls != 0 {
            t.Fatal(observed, err, stream.calls)
        }
        if !failSave {
            saved, err := store.RunObservation(input.Project.ID, submitted.ID)
            if err != nil || saved.Last != nil || saved.Failure == nil || saved.Failure.Code != "SSH_FAILED" || saved.Failure.Stage != "runner_verify" {
                t.Fatal(saved, err)
            }
        }
    }
}
