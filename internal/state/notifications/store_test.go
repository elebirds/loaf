package notifications

import (
    "context"
    "database/sql"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/eventlog"
)

func fixture(t *testing.T) (Store, Subscription, event.Page) {
    t.Helper()
    ctx := context.Background()
    store := Store{Path: filepath.Join(t.TempDir(), "local", "state.db")}
    destination := Destination{Name: "agent", Address: Address{Kind: "codex", SessionID: "thread_1"}, CreatedAt: time.Now().UTC()}
    if err := store.PutDestination(ctx, destination, false); err != nil {
        t.Fatal(err)
    }
    sub := Subscription{ID: "sub_1", Subject: event.Subject{Kind: "run", ID: "r_1"}, Source: "project", Destinations: []Destination{destination}, CreatedAt: time.Now().UTC()}
    if err := store.AddSubscription(ctx, sub, nil); err != nil {
        t.Fatal(err)
    }
    source, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "source.db"))
    if err != nil {
        t.Fatal(err)
    }
    defer source.Close()
    if _, err = source.Exec(eventlog.Schema); err != nil {
        t.Fatal(err)
    }
    tx, _ := source.Begin()
    if _, err = eventlog.Append(ctx, tx, "run:terminal", event.Envelope{Type: "run.succeeded", Subject: sub.Subject, Correlation: event.Correlation{RunID: "r_1"}, Data: event.Data{Status: "succeeded"}}); err != nil {
        t.Fatal(err)
    }
    if err = tx.Commit(); err != nil {
        t.Fatal(err)
    }
    page, err := eventlog.Read(ctx, source, event.ReadInput{Limit: 100})
    if err != nil {
        t.Fatal(err)
    }
    return store, sub, page
}

func TestCollectCommitsCursorInboxAndDeliveriesTogether(t *testing.T) {
    store, sub, page := fixture(t)
    ctx := context.Background()
    database, err := db.Open(ctx, store.Path, true)
    if err != nil {
        t.Fatal(err)
    }
    if _, err = database.Exec("CREATE TRIGGER fail_delivery BEFORE INSERT ON notify_deliveries BEGIN SELECT RAISE(ABORT,'full'); END"); err != nil {
        t.Fatal(err)
    }
    if _, err = store.Collect(ctx, sub, page); err == nil {
        t.Fatal("did not expose failed transaction")
    }
    cursor, err := store.Cursor(ctx, sub.ID)
    inbox, inboxErr := store.Inbox(ctx, event.ReadInput{Limit: 100})
    if err != nil || cursor != "" || inboxErr != nil || len(inbox.Events) != 0 {
        t.Fatal(cursor, err, inbox, inboxErr)
    }
    database.Exec("DROP TRIGGER fail_delivery")
    database.Close()
    if n, err := store.Collect(ctx, sub, page); err != nil || n != 1 {
        t.Fatal(n, err)
    }
    if n, err := store.Collect(ctx, sub, page); err != nil || n != 0 {
        t.Fatal(n, err)
    }
    deliveries, err := store.Deliveries(ctx, 100)
    if err != nil || len(deliveries) != 1 {
        t.Fatal(deliveries, err)
    }
    cursor, _ = store.Cursor(ctx, sub.ID)
    if cursor != page.NextCursor {
        t.Fatal("cursor did not commit")
    }
}

func TestInterruptedSendIsNotReplayableAndDestinationStaysFrozen(t *testing.T) {
    store, sub, page := fixture(t)
    ctx := context.Background()
    if _, err := store.Collect(ctx, sub, page); err != nil {
        t.Fatal(err)
    }
    pending, err := store.Pending(ctx, 100, time.Now())
    if err != nil || len(pending) != 1 {
        t.Fatal(pending, err)
    }
    if _, err = store.Claim(ctx, pending[0]); err != nil {
        t.Fatal(err)
    }
    if err = store.RecoverInterrupted(ctx); err != nil {
        t.Fatal(err)
    }
    pending, err = store.Pending(ctx, 100, time.Now())
    if err != nil || len(pending) != 0 {
        t.Fatal("ambiguous send became retryable", pending, err)
    }
    rows, err := store.Deliveries(ctx, 100)
    if err != nil || len(rows) != 1 || rows[0].Stage != "unconfirmed" || rows[0].Receipt.RetrySafe {
        t.Fatal(rows, err)
    }
    replacement := sub.Destinations[0]
    replacement.Address.SessionID = "other_thread"
    if err = store.PutDestination(ctx, replacement, true); err != nil {
        t.Fatal(err)
    }
    subscriptions, err := store.Subscriptions(ctx)
    if err != nil || subscriptions[0].Destinations[0].Address.SessionID != "thread_1" {
        t.Fatal("existing destination repointed", subscriptions, err)
    }
}
