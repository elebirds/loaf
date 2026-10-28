package cloud

import (
    "context"
    "encoding/json"
    "errors"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/eventlog"
)

func TestAcquisitionEventsFollowReceiptsWithoutJob(t *testing.T) {
    p := &fakeProvider{}
    s, in := serviceFixture(t, p)
    receipt, err := s.Acquire(context.Background(), in, nil)
    if err != nil {
        t.Fatal(err)
    }
    if err = s.MarkReady(context.Background(), receipt.ID, *receipt.Instance); err != nil {
        t.Fatal(err)
    }
    if err = s.MarkReady(context.Background(), receipt.ID, *receipt.Instance); err != nil {
        t.Fatal(err)
    }
    database, err := db.Open(context.Background(), filepath.Join(s.Receipts.Dir, "state.db"), false)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    page, err := eventlog.Read(context.Background(), database, event.ReadInput{Limit: 100, Subject: &event.Subject{Kind: "resource_operation", ID: receipt.ID}})
    if err != nil || len(page.Events) != 2 || page.Events[0].Type != "resource.acquired" || page.Events[1].Type != "resource.ready" {
        t.Fatal(page, err)
    }
    raw, _ := json.Marshal(page)
    for _, secret := range []string{"TEST_TOKEN", "credential", "account_settings", "ssh", "password"} {
        if strings.Contains(string(raw), secret) {
            t.Fatal("private value leaked", secret)
        }
    }
    if page.Events[1].Correlation.RunID != "" {
        t.Fatal("resource event invented a job")
    }
}

func TestWaitingRelationshipMustCommitBeforeCharge(t *testing.T) {
    p := &fakeProvider{}
    s, in := serviceFixture(t, p)
    wanted := errors.New("waiting relation could not be saved")
    called := 0
    in.BeforeMutation = func(ctx context.Context, r Receipt) error {
        called++
        saved, err := s.Receipts.Load(r.ID)
        if err != nil || saved.Status != "not_created" || p.creates != 0 {
            t.Fatal(saved, err, p.creates)
        }
        return wanted
    }
    result, err := s.Acquire(context.Background(), in, nil)
    if !errors.Is(err, wanted) || p.creates != 0 || called != 1 || result.ID == "" {
        t.Fatal(result, err, p.creates, called)
    }
}
