package remotestate

import (
    "context"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/wire"
    "testing"
    "time"
)

func TestAbandonmentAtomicallyRetainsNoStartAndPublicFact(t *testing.T) {
    root, run := fixture(t)
    s, err := Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    if err := s.CreateRun(run); err != nil {
        t.Fatal(err)
    }
    now := time.Now().UTC()
    proof := &wire.AbandonReceipt{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: "run", NoStartAt: &now, PayloadPresent: true, Device: 1, Inode: 2}
    if err := s.SaveAbandonment(proof); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Run("run"); err == nil || err.Error() != "RUN_NOT_FOUND" {
        t.Fatal("private request survived", err)
    }
    if err := s.CreateRun(run); err == nil || err.Error() != "RUN_EXISTS" {
        t.Fatal("fence lost", err)
    }
    got, err := s.Abandonment("run")
    if err != nil || got.NoStartAt == nil || got.CleanedAt != nil || !got.NoStartAt.Equal(now) {
        t.Fatal(got, err)
    }
    page, err := s.Events(context.Background(), event.ReadInput{Limit: 10})
    if err != nil || len(page.Events) != 1 || page.Events[0].Type != "run.preparation_abandoned" || page.Events[0].Data.Status != "" || page.Events[0].Data.ExitCode != nil {
        t.Fatal(page, err)
    }
    if err := s.CompleteAbandonment("run", now); err != nil {
        t.Fatal(err)
    }
    if err := s.CompleteAbandonment("run", now.Add(time.Hour)); err != nil {
        t.Fatal(err)
    }
    got, err = s.Abandonment("run")
    if err != nil || got.CleanedAt == nil || !got.CleanedAt.Equal(now) {
        t.Fatal(got, err)
    }
}

func TestAbandonmentRefusesLaunchAdmissionWithoutPID(t *testing.T) {
    root, run := fixture(t)
    s, err := Open(root, true)
    if err != nil {
        t.Fatal(err)
    }
    defer s.Close()
    run.Phase = "starting"
    if err := s.CreateRun(run); err != nil {
        t.Fatal(err)
    }
    now := time.Now().UTC()
    proof := &wire.AbandonReceipt{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: "run", NoStartAt: &now}
    if err := s.SaveAbandonment(proof); err == nil || err.Error() != "ABANDON_NOT_PREPARED" {
        t.Fatal(err)
    }
    if got, err := s.Run("run"); err != nil || got.Phase != "starting" {
        t.Fatal(got, err)
    }
    if page, err := s.Events(context.Background(), event.ReadInput{Limit: 10}); err != nil || len(page.Events) != 0 {
        t.Fatal(page, err)
    }
}
