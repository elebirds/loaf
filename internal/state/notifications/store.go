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
    "fmt"
    "os"
    "reflect"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
)

// Store uses the existing private local connection and durability policy.
type Store struct{ Path string }

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
    d.Version = RecordVersion
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
        if len(encoded) > 1<<20 || json.Unmarshal(encoded, &d) != nil || d.Version != RecordVersion || d.Name == "" || d.CreatedAt.IsZero() {
            return result, event.ErrInvalid
        }
        result = append(result, d)
    }
    if len(result) > 1000 {
        return result[:1000], errors.New("NOTIFY_LIMIT")
    }
    return result, rows.Err()
}

// Register atomically creates every source of an intent, or returns its exact prior registration.
func (s Store) Register(ctx context.Context, sub Subscription, initial map[string]event.Page) (Subscription, error) {
    sub.Version = RecordVersion
    if sub.Status == "" {
        sub.Status = "enabled"
    }
    if sub.ID == "" || sub.CreatedAt.IsZero() || sub.Subject.Validate() != nil || len(sub.Destinations) == 0 {
        return sub, event.ErrInvalid
    }
    if len(sub.Sources) == 0 {
        sub.Sources = []Source{{Source: sub.Source, Subject: sub.Subject, Binding: sub.Binding}}
    }
    for i := range sub.Sources {
        src := &sub.Sources[i]
        src.Version, src.SubscriptionID = RecordVersion, sub.ID
        if src.NextPollAt.IsZero() {
            src.NextPollAt = sub.CreatedAt
        }
        if len(sub.Sources) == 1 {
            src.ID = sub.ID
        } else {
            src.ID = fmt.Sprintf("%s_%d", sub.ID, i)
        }
        if src.Subject.Validate() != nil || (src.Source != "project" && src.Source != "queue" && src.Source != "resources") {
            return sub, event.ErrInvalid
        }
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return sub, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return sub, err
    }
    defer tx.Rollback()
    if sub.RegistrationKey != "" {
        var data []byte
        err = tx.QueryRowContext(ctx, "SELECT data FROM notify_subscriptions WHERE registration_key=?", sub.RegistrationKey).Scan(&data)
        if err == nil {
            var old Subscription
            if decode(data, &old) != nil || old.Version != RecordVersion {
                return sub, event.ErrInvalid
            }
            old.Sources, err = sourceRows(ctx, tx, "WHERE subscription_id=? ORDER BY id", old.ID)
            if err != nil {
                return sub, err
            }
            if !sameRegistration(old, sub) {
                return sub, fmt.Errorf("NOTIFY_REGISTRATION_CONFLICT")
            }
            return old, nil
        }
        if !errors.Is(err, sql.ErrNoRows) {
            return sub, err
        }
    }
    for i := range sub.Destinations {
        sub.Destinations[i].Version = RecordVersion
    }
    encoded, err := json.Marshal(sub)
    if err != nil {
        return sub, err
    }
    var key any
    if sub.RegistrationKey != "" {
        key = sub.RegistrationKey
    }
    if _, err = tx.ExecContext(ctx, "INSERT INTO notify_subscriptions VALUES(?,?,?,?)", sub.ID, key, sub.Status, encoded); err != nil {
        return sub, err
    }
    for _, src := range sub.Sources {
        data, _ := json.Marshal(src)
        if _, err = tx.ExecContext(ctx, "INSERT INTO notify_sources VALUES(?,?,?,?)", src.ID, sub.ID, stamp(src.NextPollAt), data); err != nil {
            return sub, err
        }
        if page, ok := initial[src.Source]; ok {
            if _, err = event.DecodeCursor(page.NextCursor, page.SourceID); err != nil || page.HasMore {
                return sub, event.ErrCursor
            }
            if _, err = tx.ExecContext(ctx, "INSERT INTO notify_cursors VALUES(?,?,?)", src.ID, page.SourceID, page.NextCursor); err != nil {
                return sub, err
            }
        }
    }
    return sub, tx.Commit()
}

func sameRegistration(a, b Subscription) bool {
    if a.Subject != b.Subject || len(a.Destinations) != len(b.Destinations) || len(a.Sources) != len(b.Sources) {
        return false
    }
    for i, d := range a.Destinations {
        if d.Name != b.Destinations[i].Name || d.Address != b.Destinations[i].Address {
            return false
        }
    }
    for i, src := range a.Sources {
        other := b.Sources[i]
        if src.Source != other.Source || src.Subject != other.Subject || !reflect.DeepEqual(src.Binding, other.Binding) {
            return false
        }
    }
    return true
}

func (s Store) AddSubscription(ctx context.Context, sub Subscription, initial *event.Page) error {
    pages := map[string]event.Page{}
    if initial != nil {
        pages[sub.Source] = *initial
    }
    _, err := s.Register(ctx, sub, pages)
    return err
}

type queryer interface {
    QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func decode(data []byte, v any) error {
    if len(data) > 1<<20 {
        return event.ErrInvalid
    }
    return json.Unmarshal(data, v)
}
func sourceRows(ctx context.Context, q queryer, where string, args ...any) ([]Source, error) {
    rows, err := q.QueryContext(ctx, "SELECT data FROM notify_sources "+where, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    result := []Source{}
    for rows.Next() {
        var data []byte
        var src Source
        if err = rows.Scan(&data); err != nil {
            return nil, err
        }
        if decode(data, &src) != nil || src.Version != RecordVersion || src.Subject.Validate() != nil {
            return nil, event.ErrInvalid
        }
        result = append(result, src)
    }
    return result, rows.Err()
}

// ListSubscriptions presents a keyset page of logical intents; workers query due sources instead.
func (s Store) ListSubscriptions(ctx context.Context, after string, limit int) ([]Subscription, bool, error) {
    result := []Subscription{}
    if limit < 1 || limit > 1000 {
        return result, false, event.ErrInvalid
    }
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return result, false, nil
    }
    if err != nil {
        return result, false, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_subscriptions WHERE id>? ORDER BY id LIMIT ?", after, limit+1)
    if err != nil {
        return result, false, err
    }
    for rows.Next() {
        var data []byte
        var sub Subscription
        if err = rows.Scan(&data); err != nil {
            rows.Close()
            return result, false, err
        }
        if decode(data, &sub) != nil || sub.Version != RecordVersion {
            rows.Close()
            return result, false, event.ErrInvalid
        }
        result = append(result, sub)
    }
    err = rows.Err()
    rows.Close()
    if err != nil {
        return result, false, err
    }
    truncated := len(result) > limit
    if truncated {
        result = result[:limit]
    }
    for i := range result {
        result[i].Sources, err = sourceRows(ctx, database, "WHERE subscription_id=? ORDER BY id", result[i].ID)
        if err != nil {
            return result, false, err
        }
    }
    return result, truncated, nil
}

// Subscriptions is a bounded application convenience; CLI pagination uses ListSubscriptions.
func (s Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
    rows, more, err := s.ListSubscriptions(ctx, "", 1000)
    if err == nil && more {
        err = errors.New("NOTIFY_LIMIT")
    }
    return rows, err
}

func (s Store) Cursor(ctx context.Context, bindingID string) (string, error) {
    database, err := db.Open(ctx, s.Path, false)
    if err != nil {
        return "", err
    }
    defer database.Close()
    var cursor string
    err = database.QueryRowContext(ctx, "SELECT cursor FROM notify_cursors WHERE binding_id=?", bindingID).Scan(&cursor)
    if errors.Is(err, sql.ErrNoRows) {
        return "", nil
    }
    return cursor, err
}

// DueSources advances the scheduling position before observation, not the receive cursor.
func (s Store) DueSources(ctx context.Context, limit int, now time.Time, interval time.Duration) ([]Source, error) {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return nil, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()
    sources, err := sourceRows(ctx, tx, "WHERE subscription_id IN(SELECT id FROM notify_subscriptions WHERE status='enabled') AND next_poll_at<=? ORDER BY next_poll_at,id LIMIT ?", stamp(now), limit)
    if err != nil {
        return nil, err
    }
    for i := range sources {
        sources[i].NextPollAt = now.Add(interval)
        data, _ := json.Marshal(sources[i])
        if _, err = tx.ExecContext(ctx, "UPDATE notify_sources SET next_poll_at=?,data=? WHERE id=?", stamp(sources[i].NextPollAt), data, sources[i].ID); err != nil {
            return nil, err
        }
    }
    return sources, tx.Commit()
}

// Collect atomically receives a single-source intent page.
func (s Store) Collect(ctx context.Context, sub Subscription, page event.Page) (int, error) {
    return s.CollectSource(ctx, Source{ID: sub.ID, SubscriptionID: sub.ID}, page)
}

// CollectSource commits inbox, deliveries and the exact binding cursor together.
func (s Store) CollectSource(ctx context.Context, src Source, page event.Page) (int, error) {
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
    var data []byte
    var status string
    var sub Subscription
    if err = tx.QueryRowContext(ctx, "SELECT n.data,n.status FROM notify_subscriptions n JOIN notify_sources s ON n.id=s.subscription_id WHERE s.id=?", src.ID).Scan(&data, &status); err != nil {
        return 0, err
    }
    if status != "enabled" {
        return 0, ErrInactive
    }
    if decode(data, &sub) != nil || sub.Version != RecordVersion {
        return 0, event.ErrInvalid
    }
    if err = tx.QueryRowContext(ctx, "SELECT data FROM notify_sources WHERE id=?", src.ID).Scan(&data); err != nil {
        return 0, err
    }
    if decode(data, &src) != nil || src.Version != RecordVersion {
        return 0, event.ErrInvalid
    }
    var oldSource, oldCursor string
    err = tx.QueryRowContext(ctx, "SELECT source_id,cursor FROM notify_cursors WHERE binding_id=?", src.ID).Scan(&oldSource, &oldCursor)
    if err == nil {
        old, e := event.DecodeCursor(oldCursor, oldSource)
        if e != nil || oldSource != page.SourceID || end.Sequence < old.Sequence {
            return 0, event.ErrCursor
        }
    } else if !errors.Is(err, sql.ErrNoRows) {
        return 0, err
    }
    received := 0
    for _, e := range page.Events {
        if e.Validate() != nil || e.SourceID != page.SourceID || e.Sequence > end.Sequence || !e.Matches(src.Subject) {
            return 0, event.ErrInvalid
        }
        encoded, _ := json.Marshal(e)
        inserted, err := tx.ExecContext(ctx, "INSERT INTO notify_inbox(event_id,source_id,sequence,payload) VALUES(?,?,?,?) ON CONFLICT(event_id) DO NOTHING", e.ID, e.SourceID, e.Sequence, encoded)
        if err != nil {
            return 0, err
        }
        n, _ := inserted.RowsAffected()
        if n == 0 {
            var source string
            var sequence uint64
            var retained []byte
            if err = tx.QueryRowContext(ctx, "SELECT source_id,sequence,payload FROM notify_inbox WHERE event_id=?", e.ID).Scan(&source, &sequence, &retained); err != nil {
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
            dedup, err := tx.ExecContext(ctx, "INSERT INTO notify_dedup VALUES(?,?,?,?) ON CONFLICT DO NOTHING", sub.ID, destination.Name, e.ID, id)
            if err != nil {
                return 0, err
            }
            n, _ := dedup.RowsAffected()
            if n == 0 {
                continue
            }
            d := Delivery{Version: RecordVersion, ID: id, SubscriptionID: sub.ID, Destination: destination, Event: e, Message: Message{ID: id, Subject: e.Type, Text: event.Summary(e)}, Stage: "pending", UpdatedAt: time.Now().UTC(), Receipt: Evidence{MessageID: id, State: "pending"}}
            if err = insertDelivery(ctx, tx, d); err != nil {
                return 0, err
            }
        }
    }
    if _, err = tx.ExecContext(ctx, "INSERT INTO notify_cursors VALUES(?,?,?) ON CONFLICT(binding_id) DO UPDATE SET source_id=excluded.source_id,cursor=excluded.cursor", src.ID, page.SourceID, page.NextCursor); err != nil {
        return 0, err
    }
    now := time.Now().UTC()
    src.LastSyncedAt = &now
    src.Failures = 0
    data, _ = json.Marshal(src)
    if _, err = tx.ExecContext(ctx, "UPDATE notify_sources SET data=? WHERE id=?", data, src.ID); err != nil {
        return 0, err
    }
    return received, tx.Commit()
}

var ErrInactive = errors.New("notification subscription inactive")

func stamp(t time.Time) string {
    if t.IsZero() {
        return ""
    }
    return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
func insertDelivery(ctx context.Context, tx *sql.Tx, d Delivery) error {
    if d.Stage == "pending" && d.NextAttemptAt.IsZero() {
        d.NextAttemptAt = d.UpdatedAt
    }
    data, err := json.Marshal(d)
    if err != nil {
        return err
    }
    var parent any
    if d.ParentID != "" {
        parent = d.ParentID
    }
    _, err = tx.ExecContext(ctx, "INSERT INTO notify_deliveries VALUES(?,?,?,?,?,?,?,?,?)", d.ID, d.SubscriptionID, d.Destination.Name, d.Event.ID, d.Stage, stamp(d.NextAttemptAt), stamp(d.NextLookupAt), parent, data)
    return err
}

// RecordSourceFailure keeps the receive cursor unchanged and backs off the source.
func (s Store) RecordSourceFailure(ctx context.Context, src Source, code string, interval time.Duration) error {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return err
    }
    defer database.Close()
    var data []byte
    if err = database.QueryRowContext(ctx, "SELECT data FROM notify_sources WHERE id=?", src.ID).Scan(&data); err != nil {
        return err
    }
    if decode(data, &src) != nil || src.Version != RecordVersion {
        return event.ErrInvalid
    }
    now := time.Now().UTC()
    src.SourceError = code
    src.SourceErrorAt = &now
    src.Failures++
    src.NextPollAt = now.Add(interval * time.Duration(1<<min(src.Failures, 6)))
    data, _ = json.Marshal(src)
    _, err = database.ExecContext(ctx, "UPDATE notify_sources SET next_poll_at=?,data=? WHERE id=?", stamp(src.NextPollAt), data, src.ID)
    return err
}
func deliverable(kind string) bool {
    switch kind {
    case "run.succeeded", "run.failed", "run.stopped", "run.preparation_abandoned", "queue.canceled", "queue.attention_required", "resource.acquired", "resource.ready", "resource.started", "resource.stopped", "resource.attention_required", "attention.required":
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
        if len(encoded) > 1<<20 || json.Unmarshal(encoded, &d) != nil || d.Version != RecordVersion || d.Event.Validate() != nil || d.ID == "" || d.Message.ID != d.ID {
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
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_deliveries WHERE stage='pending' AND subscription_id IN(SELECT id FROM notify_subscriptions WHERE status='enabled') AND (next_attempt_at='' OR next_attempt_at<=?) ORDER BY next_attempt_at,rowid LIMIT ?", now.UTC().Format("2006-01-02T15:04:05.000000000Z"), limit)
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
        if json.Unmarshal(encoded, &d) != nil || d.Version != RecordVersion || d.ID == "" || d.Message.ID != d.ID || d.Event.Validate() != nil {
            return result, event.ErrInvalid
        }
        result = append(result, d)
    }
    return result, rows.Err()
}

// Claim persists the possible-send boundary before calling an external target.
func (s Store) Claim(ctx context.Context, candidate Delivery) (Delivery, error) {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return candidate, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return candidate, err
    }
    defer tx.Rollback()
    var encoded []byte
    var d Delivery
    err = tx.QueryRowContext(ctx, "SELECT d.data FROM notify_deliveries d JOIN notify_subscriptions s ON d.subscription_id=s.id WHERE d.id=? AND d.stage='pending' AND s.status='enabled' AND (d.next_attempt_at='' OR d.next_attempt_at<=?)", candidate.ID, stamp(time.Now())).Scan(&encoded)
    if errors.Is(err, sql.ErrNoRows) {
        return candidate, ErrInactive
    }
    if err != nil {
        return candidate, err
    }
    if decode(encoded, &d) != nil || d.Version != RecordVersion {
        return candidate, event.ErrInvalid
    }
    d.Stage = "in_flight"
    d.Attempts++
    d.UpdatedAt = time.Now().UTC()
    d.Receipt = Evidence{MessageID: d.ID, State: "unconfirmed", ReasonCode: "DELIVERY_IN_PROGRESS"}
    encoded, _ = json.Marshal(d)
    if _, err = tx.ExecContext(ctx, "UPDATE notify_deliveries SET stage='in_flight',data=? WHERE id=?", encoded, d.ID); err != nil {
        return d, err
    }
    attempt := Attempt{Number: d.Attempts, StartedAt: d.UpdatedAt, Receipt: d.Receipt}
    encoded, _ = json.Marshal(attempt)
    if _, err = tx.ExecContext(ctx, "INSERT INTO notify_attempts VALUES(?,?,?)", d.ID, d.Attempts, encoded); err != nil {
        return d, err
    }
    return d, tx.Commit()
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
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    data, err := json.Marshal(d)
    if err != nil {
        return err
    }
    result, err := tx.ExecContext(ctx, "UPDATE notify_deliveries SET stage=?,next_attempt_at=?,next_lookup_at=?,data=? WHERE id=? AND stage=?", d.Stage, stamp(d.NextAttemptAt), stamp(d.NextLookupAt), data, d.ID, expected)
    if err != nil {
        return err
    }
    n, err := result.RowsAffected()
    if err != nil {
        return err
    }
    if n != 1 {
        return fmt.Errorf("DELIVERY_CONFLICT")
    }
    if expected == "in_flight" {
        var raw []byte
        var attempt Attempt
        if err = tx.QueryRowContext(ctx, "SELECT data FROM notify_attempts WHERE delivery_id=? AND number=?", d.ID, d.Attempts).Scan(&raw); err != nil {
            return err
        }
        if decode(raw, &attempt) != nil {
            return event.ErrInvalid
        }
        attempt.CompletedAt = &d.UpdatedAt
        attempt.Receipt = d.Receipt
        attempt.ErrorCode = d.ErrorCode
        raw, _ = json.Marshal(attempt)
        if _, err = tx.ExecContext(ctx, "UPDATE notify_attempts SET data=? WHERE delivery_id=? AND number=?", raw, d.ID, d.Attempts); err != nil {
            return err
        }
    }
    return tx.Commit()
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
        if json.Unmarshal(encoded, &d) != nil || d.Version != RecordVersion {
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
        d.NextLookupAt = d.UpdatedAt
        encoded, _ := json.Marshal(d)
        if _, err = tx.ExecContext(ctx, "UPDATE notify_deliveries SET stage='unconfirmed',next_lookup_at=?,data=? WHERE id=?", stamp(d.NextLookupAt), encoded, d.ID); err != nil {
            return err
        }
        var raw []byte
        var attempt Attempt
        if err = tx.QueryRowContext(ctx, "SELECT data FROM notify_attempts WHERE delivery_id=? AND number=?", d.ID, d.Attempts).Scan(&raw); err != nil {
            return err
        }
        if decode(raw, &attempt) != nil {
            return event.ErrInvalid
        }
        attempt.CompletedAt = &d.UpdatedAt
        attempt.Receipt = d.Receipt
        attempt.ErrorCode = d.ErrorCode
        raw, _ = json.Marshal(attempt)
        if _, err = tx.ExecContext(ctx, "UPDATE notify_attempts SET data=? WHERE delivery_id=? AND number=?", raw, d.ID, d.Attempts); err != nil {
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
