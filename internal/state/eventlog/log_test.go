package eventlog

import (
    "context"
    "database/sql"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/event"
    _ "modernc.org/sqlite"
)

func fixture(t *testing.T) *sql.DB {
    t.Helper()
    database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "events.db"))
    if err != nil {
        t.Fatal(err)
    }
    database.SetMaxOpenConns(1)
    t.Cleanup(func() { database.Close() })
    if _, err = database.Exec(Schema + "CREATE TABLE facts(id TEXT PRIMARY KEY);"); err != nil {
        t.Fatal(err)
    }
    return database
}

func appendFact(t *testing.T, database *sql.DB, key string) event.Envelope {
    t.Helper()
    ctx := context.Background()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        t.Fatal(err)
    }
    e, err := Append(ctx, tx, key, event.Envelope{Type: "resource.ready", Subject: event.Subject{Kind: "resource", ID: key}, Data: event.Data{Status: "ready"}})
    if err != nil {
        tx.Rollback()
        t.Fatal(err)
    }
    if err = tx.Commit(); err != nil {
        t.Fatal(err)
    }
    return e
}

func TestStableTransitionReplayAndSourceCursor(t *testing.T) {
    database := fixture(t)
    first := appendFact(t, database, "instance_1")
    again := appendFact(t, database, "instance_1")
    if first.ID != again.ID || first.Sequence != again.Sequence {
        t.Fatal("transition replay changed identity")
    }
    appendFact(t, database, "instance_2")
    page, err := Read(context.Background(), database, event.ReadInput{Limit: 1})
    if err != nil || len(page.Events) != 1 || !page.HasMore {
        t.Fatal(page, err)
    }
    last, err := Read(context.Background(), database, event.ReadInput{Limit: 1, Cursor: page.NextCursor})
    if err != nil || len(last.Events) != 1 || last.HasMore {
        t.Fatal(last, err)
    }
    other := fixture(t)
    if _, err = Read(context.Background(), other, event.ReadInput{Limit: 1, Cursor: page.NextCursor}); err != event.ErrCursor {
        t.Fatal("foreign cursor accepted", err)
    }
}

func TestRollbackCannotPublishEventWithoutOwningFact(t *testing.T) {
    database := fixture(t)
    ctx := context.Background()
    tx, _ := database.BeginTx(ctx, nil)
    tx.Exec("INSERT INTO facts VALUES('fact')")
    if _, err := Append(ctx, tx, "fact", event.Envelope{Type: "resource.ready", Subject: event.Subject{Kind: "resource", ID: "fact"}}); err != nil {
        t.Fatal(err)
    }
    tx.Rollback()
    page, err := Read(ctx, database, event.ReadInput{Limit: 100})
    if err != nil || len(page.Events) != 0 {
        t.Fatal(page, err)
    }
    var count int
    database.QueryRow("SELECT count(*) FROM facts").Scan(&count)
    if count != 0 {
        t.Fatal("fact escaped rollback")
    }
}

func TestFilteredCursorAdvancesPastUnrelatedFacts(t *testing.T) {
    database := fixture(t)
    appendFact(t, database, "instance_1")
    appendFact(t, database, "instance_2")
    page, err := Read(context.Background(), database, event.ReadInput{Limit: 1, Subject: &event.Subject{Kind: "resource", ID: "missing"}})
    cursor, cursorErr := event.DecodeCursor(page.NextCursor, page.SourceID)
    if err != nil || cursorErr != nil || len(page.Events) != 0 || cursor.Sequence != 2 {
        t.Fatal(page, err, cursorErr)
    }
}
