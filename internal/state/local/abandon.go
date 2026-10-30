package local

import (
    "context"
    "encoding/json"
    "errors"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/task"
)

// RecordAbandonment changes only the frozen Run's preparation availability.
// Late submission writes and rejected retries cannot erase a retained proof.
func (s *Store) RecordAbandonment(ctx context.Context, ref task.RunRef, next *task.RunAbandonment) error {
    if !task.ValidRun(ref) || next != nil && !next.Valid() {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return stateError(err)
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return stateError(err)
    }
    defer tx.Rollback()
    current, err := loadRun(ctx, tx, ref.ID)
    if err != nil {
        return stateError(err)
    }
    if !sameBinding(current, ref) || current.QueueRequestID != "" || current.Cleanup != nil || current.Submission == "submitted" && next != nil {
        return stateError(errors.New("RUN_REFERENCE_CONFLICT"))
    }
    if current.Submission == "submitted" {
        return nil
    }
    old := current.Abandonment
    if old != nil {
        if next == nil {
            if old.NoStartAt != nil || ref.Abandonment == nil || !old.RequestedAt.Equal(ref.Abandonment.RequestedAt) {
                return nil
            }
        } else {
            copy := *old
            if copy.NoStartAt == nil {
                copy.NoStartAt = next.NoStartAt
            } else if next.NoStartAt != nil && !copy.NoStartAt.Equal(*next.NoStartAt) {
                return stateError(errors.New("RUN_REFERENCE_CONFLICT"))
            }
            if copy.CleanedAt == nil {
                copy.CleanedAt = next.CleanedAt
            }
            next = &copy
        }
    }
    current.Abandonment = next
    if !task.ValidRun(current) {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    data, err := json.Marshal(current)
    if err == nil {
        _, err = tx.ExecContext(ctx, "UPDATE runs SET data=? WHERE id=?", data, current.ID)
    }
    if err == nil {
        err = tx.Commit()
    }
    return stateError(err)
}
