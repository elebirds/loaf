package notifications

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
)

func TestMultiSourceRegistrationAtomicIdempotentAndConflict(t *testing.T) {
    store, sub, _ := fixture(t)
    ctx := context.Background()
    sub.ID = "sub_group"
    sub.RegistrationKey = "queue:q_one"
    sub.Sources = []Source{{Source: "queue", Subject: event.Subject{Kind: "queue_request", ID: "q_one"}}, {Source: "project", Subject: sub.Subject}}
    database, err := db.Open(ctx, store.Path, true)
    if err != nil {
        t.Fatal(err)
    }
    _, err = database.Exec(`CREATE TRIGGER reject_project BEFORE INSERT ON notify_sources WHEN json_extract(NEW.data,'$.Source')='project' BEGIN SELECT RAISE(ABORT,'injected'); END`)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = store.Register(ctx, sub, nil); err == nil {
        t.Fatal("partial registration accepted")
    }
    var n int
    database.QueryRow("SELECT count(*) FROM notify_subscriptions WHERE id=?", sub.ID).Scan(&n)
    if n != 0 {
        t.Fatal("logical intent survived rollback")
    }
    database.QueryRow("SELECT count(*) FROM notify_sources WHERE subscription_id=?", sub.ID).Scan(&n)
    if n != 0 {
        t.Fatal("first source survived rollback")
    }
    database.Exec("DROP TRIGGER reject_project")
    database.Close()
    saved, err := store.Register(ctx, sub, nil)
    if err != nil || len(saved.Sources) != 2 {
        t.Fatal(saved, err)
    }
    sub.ID = "sub_another_allocated_id"
    again, err := store.Register(ctx, sub, nil)
    if err != nil || again.ID != saved.ID {
        t.Fatal("registration not idempotent", again, err)
    }
    sub.Sources[1].Binding.Target = "different"
    if _, err = store.Register(ctx, sub, nil); err == nil {
        t.Fatal("changed binding reused registration")
    }
}

func TestLifecycleGuardsCollectClaimAndPreservesCursors(t *testing.T) {
    store, sub, page := fixture(t)
    ctx := context.Background()
    if _, err := store.Collect(ctx, sub, page); err != nil {
        t.Fatal(err)
    }
    pending, _ := store.Pending(ctx, 10, time.Now())
    cursor, _ := store.Cursor(ctx, sub.ID)
    if _, err := store.ChangeSubscription(ctx, sub.ID, "disabled"); err != nil {
        t.Fatal(err)
    }
    if _, err := store.Claim(ctx, pending[0]); !errors.Is(err, ErrInactive) {
        t.Fatal("stale candidate sent after disable", err)
    }
    if _, err := store.Collect(ctx, sub, page); !errors.Is(err, ErrInactive) {
        t.Fatal("disabled collector advanced", err)
    }
    if err := store.RemoveDestination(ctx, "agent"); err == nil {
        t.Fatal("referenced destination removed")
    }
    if _, err := store.ChangeSubscription(ctx, sub.ID, "enabled"); err != nil {
        t.Fatal(err)
    }
    retained, _ := store.Cursor(ctx, sub.ID)
    if retained != cursor {
        t.Fatal("enable rewound source")
    }
    claimed, err := store.Claim(ctx, pending[0])
    if err != nil {
        t.Fatal(err)
    }
    changed, err := store.ChangeSubscription(ctx, sub.ID, "removed")
    if err != nil || changed.InFlight != 1 {
        t.Fatal(changed, err)
    }
    claimed.Stage = "accepted"
    claimed.Receipt = Evidence{MessageID: claimed.ID, State: "accepted", OperationID: "turn"}
    claimed.UpdatedAt = time.Now().UTC()
    if err = store.Finish(ctx, claimed); err != nil {
        t.Fatal("in-flight evidence discarded by removal", err)
    }
    if err = store.RemoveDestination(ctx, "agent"); err != nil {
        t.Fatal(err)
    }
    if _, err = store.ChangeSubscription(ctx, sub.ID, "enabled"); err == nil {
        t.Fatal("removed intent revived")
    }
}

func TestExplicitSuccessorAndResolutionNeverForgeReceipt(t *testing.T) {
    store, sub, page := fixture(t)
    ctx := context.Background()
    store.Collect(ctx, sub, page)
    rows, _ := store.Pending(ctx, 10, time.Now())
    original, _ := store.Claim(ctx, rows[0])
    store.RecoverInterrupted(ctx)
    original, _ = store.Delivery(ctx, original.ID)
    before, _ := json.Marshal(original.Receipt)
    child, err := store.Resend(ctx, original.ID)
    if err != nil || child.ID == original.ID || child.ParentID != original.ID || child.Message.Text != original.Message.Text || child.Message.ID != child.ID {
        t.Fatal(child, err)
    }
    again, err := store.Resend(ctx, original.ID)
    if err != nil || again.ID != child.ID {
        t.Fatal("duplicate command created another send", again, err)
    }
    old, _ := store.Delivery(ctx, original.ID)
    after, _ := json.Marshal(old.Receipt)
    if string(before) != string(after) || old.Stage != "resolved" || old.Resolution.Kind != "resent" {
        t.Fatal("observed receipt overwritten", old)
    }
    attempts, _ := store.Attempts(ctx, old.ID)
    if len(attempts) != 1 || attempts[0].Receipt.State != "unconfirmed" {
        t.Fatal(attempts)
    }
    d, _ := store.Claim(ctx, child)
    store.RecoverInterrupted(ctx)
    resolved, err := store.Resolve(ctx, d.ID, "confirmed")
    if err != nil || resolved.Stage != "resolved" || resolved.Receipt.State != "unconfirmed" || resolved.Receipt.Accepted() {
        t.Fatal("human decision became SDK ACK", resolved, err)
    }
}

func TestPruneRetainsDedupWithoutTouchingUnconfirmedOrRecentInbox(t *testing.T) {
    store, sub, page := fixture(t)
    ctx := context.Background()
    store.Collect(ctx, sub, page)
    pending, _ := store.Pending(ctx, 10, time.Now())
    d, _ := store.Claim(ctx, pending[0])
    d.Stage = "accepted"
    d.Receipt = Evidence{MessageID: d.ID, State: "accepted"}
    d.UpdatedAt = time.Now().Add(-time.Hour).UTC()
    if err := store.Finish(ctx, d); err != nil {
        t.Fatal(err)
    }
    before := time.Now().Add(-time.Minute)
    preview, err := store.Prune(ctx, before, true)
    if err != nil || preview.Deliveries != 1 {
        t.Fatal(preview, err)
    }
    if _, err = store.Delivery(ctx, d.ID); err != nil {
        t.Fatal("dry run deleted data")
    }
    if _, err = store.Prune(ctx, before, false); err != nil {
        t.Fatal(err)
    }
    if _, err = store.Collect(ctx, sub, page); err != nil {
        t.Fatal(err)
    }
    again, _ := store.Pending(ctx, 10, time.Now())
    if len(again) != 0 {
        t.Fatal("prune made completed fact replayable", again)
    }
    rows, _ := store.Deliveries(ctx, 10)
    if len(rows) != 0 {
        t.Fatal("history not pruned", rows)
    }
    // A fresh unconfirmed delivery is never eligible, even with a future cutoff.
    page.Events[0].ID = "evt_another"
    page.Events[0].Sequence++
    page.NextCursor, _ = event.EncodeCursor(event.Cursor{Version: 1, SourceID: page.SourceID, Sequence: page.Events[0].Sequence})
    store.Collect(ctx, sub, page)
    pending, _ = store.Pending(ctx, 10, time.Now())
    store.Claim(ctx, pending[0])
    store.RecoverInterrupted(ctx)
    future, err := store.Prune(ctx, time.Now().Add(time.Hour), false)
    if err != nil || future.Deliveries != 0 {
        t.Fatal("unconfirmed history deleted", future, err)
    }
    count, _ := store.UnconfirmedCount(ctx)
    if count != 1 {
        t.Fatal(count)
    }
}

func TestPrunePreviewAbsentStoreDoesNotInitialize(t *testing.T) {
    store := Store{Path: filepath.Join(t.TempDir(), "new", "state.db")}
    if _, err := store.Prune(context.Background(), time.Now(), true); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(filepath.Dir(store.Path)); !os.IsNotExist(err) {
        t.Fatal("preview initialized storage", err)
    }
}

func TestLookupIndexFindsOldUnknownBehindNewHistory(t *testing.T) {
    store, sub, page := fixture(t)
    ctx := context.Background()
    sub.ID = "sub_claude"
    sub.Destinations[0].Address.Kind = "claude-channel"
    sub.Destinations[0].Address.Endpoint = "/private/channel.sock"
    if err := store.AddSubscription(ctx, sub, nil); err != nil {
        t.Fatal(err)
    }
    store.Collect(ctx, sub, page)
    pending, _ := store.Pending(ctx, 10, time.Now())
    old, _ := store.Claim(ctx, pending[0])
    store.RecoverInterrupted(ctx)
    database, err := db.Open(ctx, store.Path, true)
    if err != nil {
        t.Fatal(err)
    }
    tx, _ := database.Begin()
    for i := 0; i < 220; i++ {
        id, _ := ID("d_")
        d := old
        d.ID = id
        d.Stage = "accepted"
        d.ParentID = ""
        d.Message.ID = id
        if err = insertDelivery(ctx, tx, d); err != nil {
            t.Fatal(err)
        }
    }
    if err = tx.Commit(); err != nil {
        t.Fatal(err)
    }
    database.Close()
    due, err := store.LookupDue(ctx, 200, time.Now())
    if err != nil || len(due) != 1 || due[0].ID != old.ID {
        t.Fatal("old ACK query starved", due, err)
    }
}

func TestSubscriptionPaginationIncludesArchivedRowsWithoutStarvingManagement(t *testing.T) {
    store, sub, _ := fixture(t)
    ctx := context.Background()
    sub.ID = "sub_second"
    sub.RegistrationKey = "second"
    if _, err := store.Register(ctx, sub, nil); err != nil {
        t.Fatal(err)
    }
    first, more, err := store.ListSubscriptions(ctx, "", 1)
    if err != nil || len(first) != 1 || !more {
        t.Fatal(first, more, err)
    }
    second, more, err := store.ListSubscriptions(ctx, first[0].ID, 1)
    if err != nil || len(second) != 1 || more || second[0].ID == first[0].ID {
        t.Fatal(second, more, err)
    }
}
func TestUnknownCurrentNotificationRecordIsNotRewritten(t *testing.T) {
    store, sub, page := fixture(t)
    ctx := context.Background()
    store.Collect(ctx, sub, page)
    pending, _ := store.Pending(ctx, 10, time.Now())
    d, _ := store.Claim(ctx, pending[0])
    database, err := db.Open(ctx, store.Path, true)
    if err != nil {
        t.Fatal(err)
    }
    d.Version = 99
    data, _ := json.Marshal(d)
    database.Exec("UPDATE notify_deliveries SET data=? WHERE id=?", data, d.ID)
    database.Close()
    if err = store.RecoverInterrupted(ctx); err == nil {
        t.Fatal("unknown record overwritten")
    }
    database, _ = db.Open(ctx, store.Path, false)
    defer database.Close()
    var stage string
    database.QueryRow("SELECT stage FROM notify_deliveries WHERE id=?", d.ID).Scan(&stage)
    if stage != "in_flight" {
        t.Fatal(stage)
    }
}
