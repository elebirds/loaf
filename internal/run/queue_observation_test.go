package run

import (
    "context"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type queuedObservation struct {
    Remote
    record *wire.RunRecord
}

func (q queuedObservation) Observe(context.Context, config.Target, string, wire.Request, bool) (wire.Observation, error) {
    return wire.Observation{Record: q.record}, nil
}

func TestObserveConfirmsOnlyBoundQueuedAdmissionAndRealAttempt(t *testing.T) {
    for _, mode := range []string{"confirmed", "different_queue", "missing_attempt", "not_accepted", "prepared_without_queue", "direct_preparing", "save_failed"} {
        t.Run(mode, func(t *testing.T) {
            service, input, _, _ := executionFixture(t)
            state := service.Index.(*local.Store)
            service.Observations = state
            ref := task.RunRef{ID: "r_000000000000000000000001", ProjectID: input.Project.ID, Target: input.Target, Connection: input.Connection, RemoteDir: "/remote/project", Runner: "/remote/runner", Version: wire.Version, QueueRequestID: "q_000000000000000000000001", CreatedAt: time.Now().UTC(), Submission: "preparing"}
            if mode == "direct_preparing" {
                ref.QueueRequestID = ""
            }
            if err := state.SaveRun(context.Background(), ref); err != nil {
                t.Fatal(err)
            }
            attempted := time.Now().UTC().Add(-time.Minute)
            req := request(ref, 0, 100)
            req.QueueRequestID, req.AttemptedAt = ref.QueueRequestID, &attempted
            record := &wire.RunRecord{FormatVersion: wire.FormatVersion, Request: req, Status: "running", Phase: "command", SupervisorPID: 123}
            switch mode {
            case "different_queue":
                record.Request.QueueRequestID = "q_000000000000000000000002"
            case "missing_attempt":
                record.Request.AttemptedAt = nil
            case "not_accepted":
                record.Status, record.Phase, record.SupervisorPID = "preparing", "copying", 0
            case "prepared_without_queue":
                record.Status, record.Phase, record.SupervisorPID = "preparing", "prepared", 0
                record.Request.QueueRequestID, record.Request.AttemptedAt = "", nil
            case "save_failed":
                service.Index = &failIndex{RunIndex: state, failAt: 1}
            }
            service.Remote = queuedObservation{record: record}
            observed, err := service.Observe(context.Background(), ref, 0, 100, true)
            saved, saveErr := state.Run(ref.ProjectID, ref.ID)
            if saveErr != nil {
                t.Fatal(saveErr)
            }
            switch mode {
            case "confirmed":
                if err != nil || observed.Submission != "submitted" || saved.Submission != "submitted" || saved.AttemptedAt == nil || !saved.AttemptedAt.Equal(attempted) {
                    t.Fatal(observed, saved, err)
                }
            case "save_failed":
                history, historyErr := state.RunObservation(ref.ProjectID, ref.ID)
                if err == nil || observed.Submission != "submitted" || observed.Status != "running" || observed.StateError == "" || observed.QueryError != "" || historyErr != nil || history.Last == nil || history.Failure != nil || saved.Submission != "preparing" {
                    t.Fatal(observed, saved, history, err, historyErr)
                }
            default:
                if saved.Submission != "preparing" || saved.AttemptedAt != nil || observed.Submission != "preparing" {
                    t.Fatal("unverified admission promoted", observed, saved, err)
                }
                if mode == "prepared_without_queue" && (err != nil || observed.Phase != "prepared" || observed.QueryError != "") {
                    t.Fatal("prepared input falsely rejected", observed, err)
                }
            }
            changed := saved
            changed.QueueRequestID = "q_000000000000000000000003"
            if err := state.SaveRun(context.Background(), changed); err == nil {
                t.Fatal("frozen queue identity changed")
            }
        })
    }
}
