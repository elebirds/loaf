package remotestate

import (
    "context"
    "encoding/json"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/wire"
)

func TestRunEventTransactionRollbackAndCleanupRetention(t *testing.T) {
    root, r := fixture(t)
    s, err := Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    if err = s.CreateRun(r); err != nil {
        t.Fatal(err)
    }
    r.Status, r.Phase, r.SupervisorPID = "running", "command", 123
    if err = s.SaveRun(r); err != nil {
        t.Fatal(err)
    }
    r.Phase = "setup"
    if err = s.SaveRun(r); err != nil {
        t.Fatal(err)
    }
    page, err := s.Events(context.Background(), event.ReadInput{Limit: 100})
    if err != nil || len(page.Events) != 1 || page.Events[0].Type != "run.accepted" {
        t.Fatal(page, err)
    }
    if _, err = s.db.Exec("CREATE TRIGGER event_disk_full BEFORE INSERT ON event_log BEGIN SELECT RAISE(ABORT,'full'); END"); err != nil {
        t.Fatal(err)
    }
    now := time.Now().UTC()
    r.Status, r.CompletedAt, r.ExitCode = "succeeded", &now, new(0)
    if err = s.SaveRun(r); err == nil {
        t.Fatal("event failure did not roll back state")
    }
    retained, err := s.Run(r.Request.RunID)
    if err != nil || retained.Status != "running" {
        t.Fatal(retained, err)
    }
    s.db.Exec("DROP TRIGGER event_disk_full")
    if err = s.SaveRun(r); err != nil {
        t.Fatal(err)
    }
    receipt := &wire.CleanupReceipt{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: r.Request.RunID, Summary: wire.Summary(r), Device: 1, Inode: 2}
    if err = s.SaveCleanup(receipt); err != nil {
        t.Fatal(err)
    }
    page, err = s.Events(context.Background(), event.ReadInput{Limit: 100})
    if err != nil || len(page.Events) != 2 || page.Events[1].Type != "run.succeeded" {
        t.Fatal(page, err)
    }
    data, _ := json.Marshal(page)
    if strings.Contains(string(data), "TOKEN") || strings.Contains(string(data), "private") {
        t.Fatal("private execution leaked into event", string(data))
    }
}
