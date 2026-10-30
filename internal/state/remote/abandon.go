package remotestate

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/eventlog"
    "github.com/elebirds/loaf/internal/wire"
)

// HasClaim distinguishes a never-received ID from incomplete or damaged state.
func (s *Store) HasClaim(id string) (bool, error) {
    var version string
    err := s.db.QueryRow("SELECT version FROM run_claims WHERE run_id=?", id).Scan(&version)
    if errors.Is(err, sql.ErrNoRows) {
        return false, nil
    }
    if err != nil {
        return false, errors.New("BAD_FORMAT")
    }
    if version != wire.Version {
        return false, errors.New("VERSION_MISMATCH")
    }
    return true, nil
}

// Abandonment reads the retained no-start proof independently of run payloads.
func (s *Store) Abandonment(id string) (*wire.AbandonReceipt, error) {
    r := &wire.AbandonReceipt{FormatVersion: 1, RunID: id}
    var stamp, device, inode string
    var cleaned sql.NullString
    err := s.db.QueryRow("SELECT version,root,project_id,no_start_at,cleaned_at,payload_present,device,inode FROM abandon_receipts WHERE run_id=?", id).Scan(&r.Version, &r.Root, &r.ProjectID, &stamp, &cleaned, &r.PayloadPresent, &device, &inode)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    at, err := time.Parse(time.RFC3339Nano, stamp)
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    r.NoStartAt = &at
    if cleaned.Valid {
        at, err := time.Parse(time.RFC3339Nano, cleaned.String)
        if err != nil {
            return nil, errors.New("BAD_FORMAT")
        }
        r.CleanedAt = &at
    }
    if _, err := fmt.Sscan(device, &r.Device); err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    if _, err := fmt.Sscan(inode, &r.Inode); err != nil || !r.Valid() {
        return nil, errors.New("BAD_FORMAT")
    }
    if r.Version != wire.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    return r, nil
}

// SaveAbandonment atomically fences the ID, discards private preparation data,
// and records one public fact. Callers hold project and run lifecycle locks.
func (s *Store) SaveAbandonment(r *wire.AbandonReceipt) error {
    if !r.Valid() || r.NoStartAt == nil || r.Version != wire.Version {
        return errors.New("BAD_FORMAT")
    }
    tx, err := s.db.Begin()
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    defer tx.Rollback()
    var request string
    var format, supervisor, child int
    var phase, status string
    err = tx.QueryRow("SELECT format_version,request,phase,status,supervisor_pid,child_pid FROM runs WHERE run_id=?", r.RunID).Scan(&format, &request, &phase, &status, &supervisor, &child)
    if err == nil {
        var req wire.Request
        if format != wire.FormatVersion || json.Unmarshal([]byte(request), &req) != nil || req.FormatVersion != wire.FormatVersion || req.Root != r.Root || req.ProjectID != r.ProjectID || req.RunID != r.RunID {
            return errors.New("BAD_FORMAT")
        }
        if req.Version != r.Version {
            return errors.New("VERSION_MISMATCH")
        }
        if req.QueueRequestID != "" {
            return errors.New("ABANDON_USE_QUEUE")
        }
        if (phase != "copying" && phase != "prepared") || supervisor != 0 || child != 0 || status != "starting" && !(phase == "copying" && status == "failed") {
            return errors.New("ABANDON_NOT_PREPARED")
        }
    } else if errors.Is(err, sql.ErrNoRows) {
        var count int
        if tx.QueryRow("SELECT count(*) FROM run_claims WHERE run_id=?", r.RunID).Scan(&count) != nil {
            return errors.New("BAD_FORMAT")
        }
        if count != 0 {
            return errors.New("ABANDON_UNSAFE")
        }
    } else {
        return errors.New("BAD_FORMAT")
    }
    _, err = tx.Exec("INSERT INTO run_claims VALUES(?,?,?) ON CONFLICT(run_id) DO NOTHING", r.RunID, r.Version, r.NoStartAt.UTC().Format(time.RFC3339Nano))
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    _, err = tx.Exec("INSERT INTO abandon_receipts VALUES(?,?,?,?,?,?,?,?,?)", r.RunID, r.Version, r.Root, r.ProjectID, r.NoStartAt.UTC().Format(time.RFC3339Nano), timeValue(r.CleanedAt), r.PayloadPresent, fmt.Sprint(r.Device), fmt.Sprint(r.Inode))
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    for _, table := range []string{"stop_requests", "runs"} {
        if _, err = tx.Exec("DELETE FROM "+table+" WHERE run_id=?", r.RunID); err != nil {
            return errors.New("WRITE_FAILED")
        }
    }
    _, err = eventlog.Append(context.Background(), tx, "run:"+r.RunID+":abandoned", event.Envelope{Type: "run.preparation_abandoned", OccurredAt: *r.NoStartAt, Subject: event.Subject{Kind: "run", ID: r.RunID}, Correlation: event.Correlation{ProjectID: r.ProjectID, RunID: r.RunID}, Data: event.Data{Code: "PREPARATION_ABANDONED"}})
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    if err = tx.Commit(); err != nil {
        return errors.New("WRITE_FAILED")
    }
    return nil
}

// CompleteAbandonment records deletion only after the payload directory entry
// has been durably removed. The original no-start proof is immutable.
func (s *Store) CompleteAbandonment(id string, at time.Time) error {
    result, err := s.db.Exec("UPDATE abandon_receipts SET cleaned_at=COALESCE(cleaned_at,?) WHERE run_id=?", at.UTC().Format(time.RFC3339Nano), id)
    if err != nil {
        return errors.New("ABANDON_UNCONFIRMED")
    }
    if count, err := result.RowsAffected(); err != nil || count != 1 {
        return errors.New("ABANDON_UNCONFIRMED")
    }
    return nil
}
