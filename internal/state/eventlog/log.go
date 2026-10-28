// Package eventlog stores public facts in the transaction that owns their state.
// It does not open databases or move cursors on behalf of consumers.
package eventlog

import (
    "context"
    "crypto/rand"
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "errors"
    "time"

    "github.com/elebirds/loaf/internal/event"
)

// Schema is installed only while initializing a fresh owning database.
const Schema = `
CREATE TABLE event_source (singleton INTEGER PRIMARY KEY CHECK(singleton=1), source_id TEXT NOT NULL UNIQUE);
INSERT INTO event_source VALUES(1,'src_' || lower(hex(randomblob(16))));
CREATE TABLE event_log (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    transition_key TEXT NOT NULL UNIQUE,
    subject_kind TEXT NOT NULL, subject_id TEXT NOT NULL,
    payload TEXT NOT NULL
);
CREATE INDEX event_subject ON event_log(subject_kind,subject_id,sequence);
`

// Append commits a single stable transition in the caller's transaction.
// Repeating the same transition returns the original event rather than a new ID.
func Append(ctx context.Context, tx *sql.Tx, transitionKey string, fact event.Envelope) (event.Envelope, error) {
    if transitionKey == "" || len(transitionKey) > 512 {
        return event.Envelope{}, event.ErrInvalid
    }
    var existing string
    err := tx.QueryRowContext(ctx, "SELECT payload FROM event_log WHERE transition_key=?", transitionKey).Scan(&existing)
    if err == nil {
        var e event.Envelope
        if json.Unmarshal([]byte(existing), &e) != nil || e.Validate() != nil {
            return e, event.ErrInvalid
        }
        return e, nil
    }
    if !errors.Is(err, sql.ErrNoRows) {
        return fact, err
    }
    if err := tx.QueryRowContext(ctx, "SELECT source_id FROM event_source WHERE singleton=1").Scan(&fact.SourceID); err != nil {
        return fact, err
    }
    if fact.ID == "" {
        var random [16]byte
        if _, err := rand.Read(random[:]); err != nil {
            return fact, err
        }
        fact.ID = "evt_" + hex.EncodeToString(random[:])
    }
    fact.EventVersion, fact.DataVersion = event.Version, 1
    if fact.OccurredAt.IsZero() {
        fact.OccurredAt = time.Now().UTC()
    }
    result, err := tx.ExecContext(ctx, "INSERT INTO event_log(event_id,transition_key,subject_kind,subject_id,payload) VALUES(?,?,?,?,?)", fact.ID, transitionKey, fact.Subject.Kind, fact.Subject.ID, "{}")
    if err != nil {
        return fact, err
    }
    sequence, err := result.LastInsertId()
    if err != nil {
        return fact, err
    }
    fact.Sequence = uint64(sequence)
    if err := fact.Validate(); err != nil {
        return fact, err
    }
    encoded, err := json.Marshal(fact)
    if err != nil {
        return fact, err
    }
    _, err = tx.ExecContext(ctx, "UPDATE event_log SET payload=? WHERE sequence=?", string(encoded), sequence)
    return fact, err
}

// Read returns a bounded, consistent snapshot without creating or changing state.
func Read(ctx context.Context, database *sql.DB, in event.ReadInput) (event.Page, error) {
    result := event.Page{Events: []event.Envelope{}}
    if in.Limit < 1 || in.Limit > 1000 || in.Subject != nil && in.Subject.Validate() != nil {
        return result, event.ErrInvalid
    }
    tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
    if err != nil {
        return result, err
    }
    defer tx.Rollback()
    if err := tx.QueryRowContext(ctx, "SELECT source_id FROM event_source WHERE singleton=1").Scan(&result.SourceID); err != nil {
        return result, err
    }
    var max uint64
    if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0) FROM event_log").Scan(&max); err != nil {
        return result, err
    }
    after := uint64(0)
    if in.Cursor != "" {
        cursor, err := event.DecodeCursor(in.Cursor, result.SourceID)
        if err != nil || cursor.Sequence > max {
            return result, event.ErrCursor
        }
        after = cursor.Sequence
    }
    query := "SELECT sequence,payload FROM event_log WHERE sequence>?"
    args := []any{after}
    if in.Subject != nil {
        query += " AND ((subject_kind=? AND subject_id=?)"
        args = append(args, in.Subject.Kind, in.Subject.ID)
        field := ""
        switch in.Subject.Kind {
        case "run":
            field = "run_id"
        case "queue_request":
            field = "queue_request_id"
        case "resource_operation":
            field = "cloud_request_id"
        }
        if field != "" {
            query += " OR json_extract(payload,'$.correlation." + field + "')=?"
            args = append(args, in.Subject.ID)
        }
        query += ")"
    }
    query += " ORDER BY sequence LIMIT ?"
    args = append(args, in.Limit+1)
    rows, err := tx.QueryContext(ctx, query, args...)
    if err != nil {
        return result, err
    }
    for rows.Next() {
        var sequence uint64
        var payload string
        if err := rows.Scan(&sequence, &payload); err != nil {
            rows.Close()
            return result, err
        }
        if len(result.Events) == in.Limit {
            result.HasMore = true
            break
        }
        var e event.Envelope
        if json.Unmarshal([]byte(payload), &e) != nil || e.Validate() != nil || e.SourceID != result.SourceID || e.Sequence != sequence {
            rows.Close()
            return result, event.ErrInvalid
        }
        result.Events = append(result.Events, e)
        after = sequence
    }
    err = rows.Err()
    rows.Close()
    if err != nil {
        return result, err
    }
    if !result.HasMore {
        after = max
    }
    result.NextCursor, err = event.EncodeCursor(event.Cursor{Version: event.Version, SourceID: result.SourceID, Sequence: after})
    if err != nil {
        return result, err
    }
    if err = tx.Commit(); err != nil {
        return result, err
    }
    return result, nil
}
