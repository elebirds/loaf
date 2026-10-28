// Package notifications persists private destination bindings, waiting relations
// and public receipt evidence in the local control database.
package notifications

import (
    "bytes"
    "context"
    "crypto/rand"
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "errors"
    "os"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

// Store uses the existing private local connection and durability policy.
type Store struct{ Path string }

// Destination is a private, immutable snapshot once attached to a subscription.
type Destination struct {
    Name      string
    Address   agentdelivery.Destination
    CreatedAt time.Time
}

// Subscription holds an explicit source binding; alias edits cannot repoint it.
type Subscription struct {
    ID            string
    Subject       event.Subject
    Source        string
    Binding       task.RunRef
    Destinations  []Destination
    CreatedAt     time.Time
    LastSyncedAt  *time.Time
    SourceError   string
    SourceErrorAt *time.Time
}

// Delivery combines public evidence with the private frozen recipient and fact.
type Delivery struct {
    ID             string
    SubscriptionID string
    Destination    Destination
    Event          event.Envelope
    Stage          string
    Receipt        agentdelivery.Receipt
    Attempts       int
    UpdatedAt      time.Time
    NextAttemptAt  time.Time
    ErrorCode      string
}

// ID allocates an opaque local identity without encoding private values.
func ID(prefix string) (string, error) {
    var random [16]byte
    if _, err := rand.Read(random[:]); err != nil {
        return "", err
    }
    return prefix + hex.EncodeToString(random[:]), nil
}

// PutDestination saves a validated binding without touching an Agent.
func (s Store) PutDestination(ctx context.Context, d Destination, replace bool) error {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return err
    }
    defer database.Close()
    encoded, err := json.Marshal(d)
    if err != nil {
        return err
    }
    query := "INSERT INTO notify_destinations(name,data) VALUES(?,?)"
    if replace {
        query += " ON CONFLICT(name) DO UPDATE SET data=excluded.data"
    }
    _, err = database.ExecContext(ctx, query, d.Name, encoded)
    return err
}

// Destinations returns bounded local bindings for composition, never presentation.
func (s Store) Destinations(ctx context.Context) ([]Destination, error) {
    result := []Destination{}
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return result, nil
    }
    if err != nil {
        return result, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_destinations ORDER BY name LIMIT 1001")
    if err != nil {
        return result, err
    }
    defer rows.Close()
    for rows.Next() {
        var encoded []byte
        var d Destination
        if err = rows.Scan(&encoded); err != nil {
            return result, err
        }
        if len(encoded) > 1<<20 || json.Unmarshal(encoded, &d) != nil || d.Address.Validate() != nil || d.Name == "" || d.CreatedAt.IsZero() {
            return result, event.ErrInvalid
        }
        result = append(result, d)
    }
    if len(result) > 1000 {
        return result[:1000], errors.New("NOTIFY_LIMIT")
    }
    return result, rows.Err()
}

// AddSubscription commits the waiting relation and optional initial source head.
func (s Store) AddSubscription(ctx context.Context, sub Subscription, initial *event.Page) error {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    encoded, err := json.Marshal(sub)
    if err != nil {
        return err
    }
    if _, err = tx.ExecContext(ctx, "INSERT INTO notify_subscriptions VALUES(?,?,?,?,?)", sub.ID, sub.Subject.Kind, sub.Subject.ID, sub.Source, encoded); err != nil {
        return err
    }
    if initial != nil {
        if _, err = event.DecodeCursor(initial.NextCursor, initial.SourceID); err != nil || initial.HasMore {
            return event.ErrCursor
        }
        if _, err = tx.ExecContext(ctx, "INSERT INTO notify_cursors VALUES(?,?,?)", sub.ID, initial.SourceID, initial.NextCursor); err != nil {
            return err
        }
    }
    return tx.Commit()
}

// Subscriptions returns every bounded, private source/recipient relationship.
func (s Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
    result := []Subscription{}
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return result, nil
    }
    if err != nil {
        return result, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_subscriptions ORDER BY id LIMIT 1001")
    if err != nil {
        return result, err
    }
    defer rows.Close()
    for rows.Next() {
        var encoded []byte
        var sub Subscription
        if err = rows.Scan(&encoded); err != nil {
            return result, err
        }
        if len(encoded) > 1<<20 || json.Unmarshal(encoded, &sub) != nil || sub.Subject.Validate() != nil || sub.ID == "" || len(sub.Destinations) == 0 {
            return result, event.ErrInvalid
        }
        result = append(result, sub)
    }
    if len(result) > 1000 {
        return result[:1000], errors.New("NOTIFY_LIMIT")
    }
    return result, rows.Err()
}

// Cursor reads only the persisted, successfully received source position.
func (s Store) Cursor(ctx context.Context, subscriptionID string) (string, error) {
    database, err := db.Open(ctx, s.Path, false)
    if err != nil {
        return "", err
    }
    defer database.Close()
    var cursor string
    err = database.QueryRowContext(ctx, "SELECT cursor FROM notify_cursors WHERE subscription_id=?", subscriptionID).Scan(&cursor)
    if errors.Is(err, sql.ErrNoRows) {
        return "", nil
    }
    return cursor, err
}

// Collect commits inbox, deliveries and receive cursor in one transaction.
func (s Store) Collect(ctx context.Context, sub Subscription, page event.Page) (int, error) {
    end, err := event.DecodeCursor(page.NextCursor, page.SourceID)
    if err != nil {
        return 0, err
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return 0, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return 0, err
    }
    defer tx.Rollback()
    var oldSource, oldCursor string
    err = tx.QueryRowContext(ctx, "SELECT source_id,cursor FROM notify_cursors WHERE subscription_id=?", sub.ID).Scan(&oldSource, &oldCursor)
    if err == nil {
        old, decodeErr := event.DecodeCursor(oldCursor, oldSource)
        if decodeErr != nil || oldSource != page.SourceID || end.Sequence < old.Sequence {
            return 0, event.ErrCursor
        }
    } else if !errors.Is(err, sql.ErrNoRows) {
        return 0, err
    }
    received := 0
    for _, e := range page.Events {
        if e.Validate() != nil || e.SourceID != page.SourceID || e.Sequence > end.Sequence || !e.Matches(sub.Subject) {
            return 0, event.ErrInvalid
        }
        encoded, err := json.Marshal(e)
        if err != nil {
            return 0, err
        }
        result, err := tx.ExecContext(ctx, "INSERT INTO notify_inbox(event_id,source_id,sequence,payload) VALUES(?,?,?,?) ON CONFLICT(event_id) DO NOTHING", e.ID, e.SourceID, e.Sequence, encoded)
        if err != nil {
            return 0, err
        }
        n, _ := result.RowsAffected()
        if n == 0 {
            var source string
            var sequence uint64
            var retained []byte
            if err := tx.QueryRowContext(ctx, "SELECT source_id,sequence,payload FROM notify_inbox WHERE event_id=?", e.ID).Scan(&source, &sequence, &retained); err != nil {
                return 0, err
            }
            if source != e.SourceID || sequence != e.Sequence || !bytes.Equal(retained, encoded) {
                return 0, event.ErrInvalid
            }
        }
        received += int(n)
        if !deliverable(e.Type) {
            continue
        }
        for _, destination := range sub.Destinations {
            id, err := ID("d_")
            if err != nil {
                return 0, err
            }
            d := Delivery{ID: id, SubscriptionID: sub.ID, Destination: destination, Event: e, Stage: "pending", UpdatedAt: time.Now().UTC(), Receipt: agentdelivery.Receipt{MessageID: id, State: "pending"}}
            data, err := json.Marshal(d)
            if err != nil {
                return 0, err
            }
            if _, err = tx.ExecContext(ctx, "INSERT INTO notify_deliveries VALUES(?,?,?,?,?,?,?) ON CONFLICT(subscription_id,destination,event_id) DO NOTHING", d.ID, sub.ID, destination.Name, e.ID, d.Stage, "", data); err != nil {
                return 0, err
            }
        }
    }
    if _, err = tx.ExecContext(ctx, "INSERT INTO notify_cursors VALUES(?,?,?) ON CONFLICT(subscription_id) DO UPDATE SET source_id=excluded.source_id,cursor=excluded.cursor", sub.ID, page.SourceID, page.NextCursor); err != nil {
        return 0, err
    }
    synced := time.Now().UTC()
    sub.LastSyncedAt = &synced
    encoded, err := json.Marshal(sub)
    if err != nil {
        return 0, err
    }
    if _, err = tx.ExecContext(ctx, "UPDATE notify_subscriptions SET data=? WHERE id=?", encoded, sub.ID); err != nil {
        return 0, err
    }
    if err = tx.Commit(); err != nil {
        return 0, err
    }
    return received, nil
}

// RecordSourceError retains a safe read failure without advancing its cursor.
func (s Store) RecordSourceError(ctx context.Context, sub Subscription, code string) error {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return err
    }
    defer database.Close()
    observed := time.Now().UTC()
    sub.SourceError, sub.SourceErrorAt = code, &observed
    data, err := json.Marshal(sub)
    if err != nil {
        return err
    }
    _, err = database.ExecContext(ctx, "UPDATE notify_subscriptions SET data=? WHERE id=?", data, sub.ID)
    return err
}

func deliverable(kind string) bool {
    switch kind {
    case "run.succeeded", "run.failed", "run.stopped", "queue.canceled", "queue.attention_required", "resource.acquired", "resource.ready", "resource.started", "resource.stopped", "resource.attention_required", "attention.required":
        return true
    }
    return false
}

// Deliveries returns bounded public evidence with private recipient bindings.
func (s Store) Deliveries(ctx context.Context, limit int) ([]Delivery, error) {
    result := []Delivery{}
    if limit < 1 || limit > 1000 {
        return result, event.ErrInvalid
    }
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return result, nil
    }
    if err != nil {
        return result, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_deliveries ORDER BY rowid DESC LIMIT ?", limit)
    if err != nil {
        return result, err
    }
    defer rows.Close()
    for rows.Next() {
        var encoded []byte
        var d Delivery
        if err = rows.Scan(&encoded); err != nil {
            return result, err
        }
        if len(encoded) > 1<<20 || json.Unmarshal(encoded, &d) != nil || d.Event.Validate() != nil || d.ID == "" {
            return result, event.ErrInvalid
        }
        result = append(result, d)
    }
    return result, rows.Err()
}

// Pending retains dispatch order and does not scan past a bounded batch.
func (s Store) Pending(ctx context.Context, limit int, now time.Time) ([]Delivery, error) {
    result := []Delivery{}
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return result, nil
    }
    if err != nil {
        return result, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_deliveries WHERE stage='pending' AND (next_attempt_at='' OR next_attempt_at<=?) ORDER BY rowid LIMIT ?", now.UTC().Format("2006-01-02T15:04:05.000000000Z"), limit)
    if err != nil {
        return result, err
    }
    defer rows.Close()
    for rows.Next() {
        var encoded []byte
        var d Delivery
        if err = rows.Scan(&encoded); err != nil {
            return result, err
        }
        if json.Unmarshal(encoded, &d) != nil || d.ID == "" || d.Event.Validate() != nil {
            return result, event.ErrInvalid
        }
        result = append(result, d)
    }
    return result, rows.Err()
}

// Claim persists the possible-send boundary before calling an external target.
func (s Store) Claim(ctx context.Context, d Delivery) (Delivery, error) {
    d.Stage = "in_flight"
    d.Attempts++
    d.UpdatedAt = time.Now().UTC()
    d.Receipt = agentdelivery.Receipt{MessageID: d.ID, State: "unconfirmed", ReasonCode: "DELIVERY_IN_PROGRESS"}
    return d, s.update(ctx, d, "pending")
}

// Finish preserves strong evidence; only a proven safe retry becomes pending.
func (s Store) Finish(ctx context.Context, d Delivery) error { return s.update(ctx, d, "in_flight") }

// Reconcile strengthens an unknown outcome with newly observed receipt evidence.
func (s Store) Reconcile(ctx context.Context, d Delivery) error {
    return s.update(ctx, d, "unconfirmed")
}

// PendingCount includes deferred safe retries, not only immediately due work.
func (s Store) PendingCount(ctx context.Context) (int, error) {
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return 0, nil
    }
    if err != nil {
        return 0, err
    }
    defer database.Close()
    var count int
    err = database.QueryRowContext(ctx, "SELECT count(*) FROM notify_deliveries WHERE stage='pending'").Scan(&count)
    return count, err
}

// UnconfirmedCount reports retained ambiguous sends, including earlier rounds.
func (s Store) UnconfirmedCount(ctx context.Context) (int, error) {
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return 0, nil
    }
    if err != nil {
        return 0, err
    }
    defer database.Close()
    var count int
    err = database.QueryRowContext(ctx, "SELECT count(*) FROM notify_deliveries WHERE stage='unconfirmed'").Scan(&count)
    return count, err
}

func (s Store) update(ctx context.Context, d Delivery, expected string) error {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return err
    }
    defer database.Close()
    encoded, err := json.Marshal(d)
    if err != nil {
        return err
    }
    next := ""
    if !d.NextAttemptAt.IsZero() {
        next = d.NextAttemptAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
    }
    result, err := database.ExecContext(ctx, "UPDATE notify_deliveries SET stage=?,next_attempt_at=?,data=? WHERE id=? AND stage=?", d.Stage, next, encoded, d.ID, expected)
    if err != nil {
        return err
    }
    n, err := result.RowsAffected()
    if err != nil {
        return err
    }
    if n != 1 {
        return errors.New("DELIVERY_CONFLICT")
    }
    return nil
}

// RecoverInterrupted marks old in-flight attempts unknown, never automatically replayable.
// The caller must own the Bridge's OS lifetime lock.
func (s Store) RecoverInterrupted(ctx context.Context) error {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    rows, err := tx.QueryContext(ctx, "SELECT data FROM notify_deliveries WHERE stage='in_flight'")
    if err != nil {
        return err
    }
    var interrupted []Delivery
    for rows.Next() {
        var encoded []byte
        var d Delivery
        if err = rows.Scan(&encoded); err != nil {
            rows.Close()
            return err
        }
        if json.Unmarshal(encoded, &d) != nil {
            rows.Close()
            return event.ErrInvalid
        }
        interrupted = append(interrupted, d)
    }
    err = rows.Err()
    rows.Close()
    if err != nil {
        return err
    }
    for _, d := range interrupted {
        d.Stage = "unconfirmed"
        d.ErrorCode = "DELIVERY_UNCONFIRMED"
        d.Receipt.State = "unconfirmed"
        d.Receipt.ReasonCode = "DELIVERY_INTERRUPTED"
        d.Receipt.RetrySafe = false
        d.UpdatedAt = time.Now().UTC()
        encoded, _ := json.Marshal(d)
        if _, err = tx.ExecContext(ctx, "UPDATE notify_deliveries SET stage='unconfirmed',data=? WHERE id=?", encoded, d.ID); err != nil {
            return err
        }
    }
    return tx.Commit()
}

// Inbox reads local receive order while keeping each envelope's original identity.
func (s Store) Inbox(ctx context.Context, in event.ReadInput) (event.Page, error) {
    result := event.Page{Events: []event.Envelope{}}
    if in.Limit < 1 || in.Limit > 1000 || in.Subject != nil && in.Subject.Validate() != nil {
        return result, event.ErrInvalid
    }
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return result, nil
    }
    if err != nil {
        return result, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
    if err != nil {
        return result, err
    }
    defer tx.Rollback()
    if err = tx.QueryRowContext(ctx, "SELECT source_id || ':inbox' FROM event_source WHERE singleton=1").Scan(&result.SourceID); err != nil {
        return result, err
    }
    after := uint64(0)
    if in.Cursor != "" {
        cursor, err := event.DecodeCursor(in.Cursor, result.SourceID)
        if err != nil {
            return result, err
        }
        after = cursor.Sequence
    }
    var max uint64
    if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(received_sequence),0) FROM notify_inbox").Scan(&max); err != nil {
        return result, err
    }
    if after > max {
        return result, event.ErrCursor
    }
    query := "SELECT received_sequence,payload FROM notify_inbox WHERE received_sequence>?"
    args := []any{after}
    if in.Subject != nil {
        query += " AND ((json_extract(payload,'$.subject.kind')=? AND json_extract(payload,'$.subject.id')=?)"
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
    query += " ORDER BY received_sequence LIMIT ?"
    args = append(args, in.Limit+1)
    rows, err := tx.QueryContext(ctx, query, args...)
    if err != nil {
        return result, err
    }
    for rows.Next() {
        var sequence uint64
        var encoded []byte
        if err = rows.Scan(&sequence, &encoded); err != nil {
            rows.Close()
            return result, err
        }
        if len(result.Events) == in.Limit {
            result.HasMore = true
            break
        }
        var e event.Envelope
        if json.Unmarshal(encoded, &e) != nil || e.Validate() != nil {
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
    return result, tx.Commit()
}
