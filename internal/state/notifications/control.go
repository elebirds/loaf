package notifications

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
    "os"
    "time"
)

// Delivery reads one retained, version-checked record.
func (s Store) Delivery(ctx context.Context, id string) (Delivery, error) {
    var d Delivery
    database, err := db.Open(ctx, s.Path, false)
    if err != nil {
        return d, err
    }
    defer database.Close()
    var data []byte
    err = database.QueryRowContext(ctx, "SELECT data FROM notify_deliveries WHERE id=?", id).Scan(&data)
    if err != nil {
        return d, err
    }
    if decode(data, &d) != nil || d.Version != RecordVersion || d.Event.Validate() != nil {
        return d, event.ErrInvalid
    }
    return d, nil
}

// Attempts returns every possible-send boundary in order for one explicit delivery.
func (s Store) Attempts(ctx context.Context, id string) ([]Attempt, error) {
    result := []Attempt{}
    database, err := db.Open(ctx, s.Path, false)
    if err != nil {
        return result, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_attempts WHERE delivery_id=? ORDER BY number", id)
    if err != nil {
        return result, err
    }
    defer rows.Close()
    for rows.Next() {
        var data []byte
        var a Attempt
        if err = rows.Scan(&data); err != nil {
            return result, err
        }
        if decode(data, &a) != nil {
            return result, event.ErrInvalid
        }
        result = append(result, a)
    }
    return result, rows.Err()
}

// Subscription reads one logical intent including all frozen source bindings.
func (s Store) Subscription(ctx context.Context, id string) (Subscription, error) {
    var sub Subscription
    database, err := db.Open(ctx, s.Path, false)
    if err != nil {
        return sub, err
    }
    defer database.Close()
    var data []byte
    err = database.QueryRowContext(ctx, "SELECT data FROM notify_subscriptions WHERE id=?", id).Scan(&data)
    if err != nil {
        return sub, err
    }
    if decode(data, &sub) != nil || sub.Version != RecordVersion {
        return sub, event.ErrInvalid
    }
    sub.Sources, err = sourceRows(ctx, database, "WHERE subscription_id=? ORDER BY id", id)
    return sub, err
}

// ChangeSubscription serializes lifecycle changes against both collection and Claim.
func (s Store) ChangeSubscription(ctx context.Context, id, status string) (SubscriptionChange, error) {
    result := SubscriptionChange{SubscriptionID: id, Status: status}
    if status != "enabled" && status != "disabled" && status != "removed" {
        return result, event.ErrInvalid
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return result, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return result, err
    }
    defer tx.Rollback()
    var data []byte
    var sub Subscription
    if err = tx.QueryRowContext(ctx, "SELECT data FROM notify_subscriptions WHERE id=?", id).Scan(&data); err != nil {
        return result, err
    }
    if decode(data, &sub) != nil || sub.Version != RecordVersion {
        return result, event.ErrInvalid
    }
    if sub.Status == "removed" && status != "removed" {
        return result, ErrInactive
    }
    sub.Status = status
    data, _ = json.Marshal(sub)
    if _, err = tx.ExecContext(ctx, "UPDATE notify_subscriptions SET status=?,data=? WHERE id=?", status, data, id); err != nil {
        return result, err
    }
    if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM notify_deliveries WHERE subscription_id=? AND stage='in_flight'", id).Scan(&result.InFlight); err != nil {
        return result, err
    }
    if status == "enabled" {
        if _, err = tx.ExecContext(ctx, "UPDATE notify_sources SET next_poll_at=? WHERE subscription_id=?", stamp(time.Now().UTC()), id); err != nil {
            return result, err
        }
    }
    if status == "removed" {
        rows, err := tx.QueryContext(ctx, "SELECT data FROM notify_deliveries WHERE subscription_id=? AND stage='pending'", id)
        if err != nil {
            return result, err
        }
        var pending []Delivery
        for rows.Next() {
            var raw []byte
            var d Delivery
            if err = rows.Scan(&raw); err != nil {
                rows.Close()
                return result, err
            }
            if decode(raw, &d) != nil {
                rows.Close()
                return result, event.ErrInvalid
            }
            pending = append(pending, d)
        }
        err = rows.Err()
        rows.Close()
        if err != nil {
            return result, err
        }
        for _, d := range pending {
            d.Stage = "resolved"
            d.UpdatedAt = time.Now().UTC()
            d.Resolution = &Resolution{Kind: "abandoned", At: d.UpdatedAt}
            data, _ = json.Marshal(d)
            if _, err = tx.ExecContext(ctx, "UPDATE notify_deliveries SET stage='resolved',data=? WHERE id=?", data, d.ID); err != nil {
                return result, err
            }
        }
    }
    return result, tx.Commit()
}

// DestinationInUse lists the exact subscriptions preventing removal.
type DestinationInUse struct{ IDs []string }

func (e *DestinationInUse) Error() string { return "DESTINATION_IN_USE" }

// RemoveDestination removes only an unreferenced catalog entry; frozen history stays intact.
func (s Store) RemoveDestination(ctx context.Context, name string) error {
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
    rows, err := tx.QueryContext(ctx, "SELECT DISTINCT s.id FROM notify_subscriptions s,json_each(s.data,'$.Destinations') d WHERE s.status!='removed' AND json_extract(d.value,'$.Name')=? ORDER BY s.id", name)
    if err != nil {
        return err
    }
    ids := []string{}
    for rows.Next() {
        var id string
        if err = rows.Scan(&id); err != nil {
            rows.Close()
            return err
        }
        ids = append(ids, id)
    }
    err = rows.Err()
    rows.Close()
    if err != nil {
        return err
    }
    if len(ids) > 0 {
        return &DestinationInUse{IDs: ids}
    }
    if _, err = tx.ExecContext(ctx, "DELETE FROM notify_destinations WHERE name=?", name); err != nil {
        return err
    }
    return tx.Commit()
}

// Resolve records user judgment without strengthening observed SDK evidence.
func (s Store) Resolve(ctx context.Context, id, kind string) (Delivery, error) {
    var d Delivery
    if kind != "confirmed" && kind != "abandoned" {
        return d, event.ErrInvalid
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return d, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return d, err
    }
    defer tx.Rollback()
    var data []byte
    if err = tx.QueryRowContext(ctx, "SELECT data FROM notify_deliveries WHERE id=?", id).Scan(&data); err != nil {
        return d, err
    }
    if decode(data, &d) != nil || d.Version != RecordVersion {
        return d, event.ErrInvalid
    }
    if d.Stage == "in_flight" {
        return d, fmt.Errorf("DELIVERY_BUSY")
    }
    if d.Resolution != nil {
        if d.Resolution.Kind == kind {
            return d, nil
        }
        return d, fmt.Errorf("DELIVERY_CONFLICT")
    }
    if d.Stage == "accepted" || d.Stage == "processed" {
        return d, fmt.Errorf("DELIVERY_ALREADY_ACCEPTED")
    }
    d.Stage = "resolved"
    d.UpdatedAt = time.Now().UTC()
    d.Resolution = &Resolution{Kind: kind, At: d.UpdatedAt}
    data, _ = json.Marshal(d)
    if _, err = tx.ExecContext(ctx, "UPDATE notify_deliveries SET stage=?,data=? WHERE id=?", d.Stage, data, id); err != nil {
        return d, err
    }
    return d, tx.Commit()
}

// Resend creates at most one successor per explicit parent, without sending it.
func (s Store) Resend(ctx context.Context, id string) (Delivery, error) {
    var d Delivery
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return d, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return d, err
    }
    defer tx.Rollback()
    var data []byte
    err = tx.QueryRowContext(ctx, "SELECT data FROM notify_deliveries WHERE parent_id=?", id).Scan(&data)
    if err == nil {
        if decode(data, &d) != nil || d.Version != RecordVersion {
            return d, event.ErrInvalid
        }
        return d, nil
    }
    if !errors.Is(err, sql.ErrNoRows) {
        return d, err
    }
    if err = tx.QueryRowContext(ctx, "SELECT d.data FROM notify_deliveries d JOIN notify_subscriptions s ON s.id=d.subscription_id WHERE d.id=? AND s.status='enabled'", id).Scan(&data); err != nil {
        return d, err
    }
    if decode(data, &d) != nil || d.Version != RecordVersion {
        return d, event.ErrInvalid
    }
    if d.Stage != "unconfirmed" && d.Stage != "rejected" {
        return d, fmt.Errorf("DELIVERY_NOT_RESENDABLE")
    }
    successor := d
    successor.ID, err = ID("d_")
    if err != nil {
        return d, err
    }
    successor.ParentID = d.ID
    successor.Stage = "pending"
    successor.Attempts = 0
    successor.ErrorCode = ""
    successor.Resolution = nil
    successor.UpdatedAt = time.Now().UTC()
    successor.NextAttemptAt = successor.UpdatedAt
    successor.NextLookupAt = time.Time{}
    successor.Message.ID = successor.ID
    successor.Receipt = Evidence{MessageID: successor.ID, State: "pending"}
    if err = insertDelivery(ctx, tx, successor); err != nil {
        return d, err
    }
    d.Stage = "resolved"
    d.UpdatedAt = successor.UpdatedAt
    d.Resolution = &Resolution{Kind: "resent", At: d.UpdatedAt, SuccessorID: successor.ID}
    data, _ = json.Marshal(d)
    if _, err = tx.ExecContext(ctx, "UPDATE notify_deliveries SET stage='resolved',data=? WHERE id=?", data, id); err != nil {
        return d, err
    }
    return successor, tx.Commit()
}

// LookupDue uses its own actionable index, independent of recent delivery history.
func (s Store) LookupDue(ctx context.Context, limit int, now time.Time) ([]Delivery, error) {
    result := []Delivery{}
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return result, nil
    }
    if err != nil {
        return result, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM notify_deliveries WHERE stage='unconfirmed' AND next_lookup_at<=? AND json_extract(data,'$.Destination.Address.Kind')='claude-channel' ORDER BY next_lookup_at,id LIMIT ?", stamp(now), limit)
    if err != nil {
        return result, err
    }
    defer rows.Close()
    for rows.Next() {
        var data []byte
        var d Delivery
        if err = rows.Scan(&data); err != nil {
            return result, err
        }
        if decode(data, &d) != nil || d.Version != RecordVersion {
            return result, event.ErrInvalid
        }
        result = append(result, d)
    }
    return result, rows.Err()
}

// ScheduleLookup persists fairness before a potentially slow read, never changes receipt evidence.
func (s Store) ScheduleLookup(ctx context.Context, d Delivery, next time.Time) (Delivery, error) {
    d.NextLookupAt = next
    return d, s.update(ctx, d, "unconfirmed")
}

// RejectedCount includes last observed rejections, including pending safe retries, for enabled intents.
func (s Store) RejectedCount(ctx context.Context) (int, error) {
    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return 0, nil
    }
    if err != nil {
        return 0, err
    }
    defer database.Close()
    var n int
    err = database.QueryRowContext(ctx, "SELECT count(*) FROM notify_deliveries WHERE (stage='rejected' OR (stage='pending' AND json_extract(data,'$.Receipt.state')='rejected')) AND subscription_id IN(SELECT id FROM notify_subscriptions WHERE status='enabled')").Scan(&n)
    return n, err
}

// Prune deletes settled payload/history only. Compact root dedup markers survive forever.
func (s Store) Prune(ctx context.Context, before time.Time, dry bool) (PruneResult, error) {
    result := PruneResult{DryRun: dry, Before: before}
    if before.IsZero() {
        return result, event.ErrInvalid
    }
    database, err := db.Open(ctx, s.Path, !dry)
    if os.IsNotExist(err) && dry {
        return result, nil
    }
    if err != nil {
        return result, err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: dry})
    if err != nil {
        return result, err
    }
    defer tx.Rollback()
    filter := `stage IN('accepted','processed','resolved','rejected') AND julianday(json_extract(data,'$.UpdatedAt'))<julianday(?)`
    if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM notify_deliveries WHERE "+filter, stamp(before)).Scan(&result.Deliveries); err != nil {
        return result, err
    }
    if dry {
        err = tx.QueryRowContext(ctx, "SELECT count(*) FROM notify_inbox WHERE julianday(json_extract(payload,'$.occurred_at'))<julianday(?) AND event_id NOT IN(SELECT event_id FROM notify_deliveries WHERE NOT("+filter+"))", stamp(before), stamp(before)).Scan(&result.Inbox)
        return result, err
    }
    if _, err = tx.ExecContext(ctx, "DELETE FROM notify_deliveries WHERE "+filter, stamp(before)); err != nil {
        return result, err
    }
    deleted, err := tx.ExecContext(ctx, "DELETE FROM notify_inbox WHERE julianday(json_extract(payload,'$.occurred_at'))<julianday(?) AND event_id NOT IN(SELECT event_id FROM notify_deliveries)", stamp(before))
    if err != nil {
        return result, err
    }
    n, _ := deleted.RowsAffected()
    result.Inbox = int(n)
    return result, tx.Commit()
}
