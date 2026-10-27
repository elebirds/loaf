package local

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

func reference(n int) task.RunRef {
    now := time.Now().UTC()
    return task.RunRef{ID: fmt.Sprintf("r_%024x", n), ProjectID: "p_000000000000000000000001", Target: "lab", Connection: config.Target{Destination: "lab", RemoteBase: "~/projects"}, RemoteDir: "/tmp/project", Runner: "/tmp/runner", Version: "dev", CreatedAt: now, Submission: "preparing"}
}

func TestRunLocatorsAreLockedAndDoNotPromotePreparation(t *testing.T) {
    store := testStore(t)
    var wg sync.WaitGroup
    for n := 0; n < 12; n++ {
        wg.Add(1)
        go func(n int) {
            defer wg.Done()
            ref := reference(n)
            if n != 0 {
                ref.Submission = "uncertain"
                ref.AttemptedAt = &ref.CreatedAt
            }
            if err := store.SaveRun(context.Background(), ref); err != nil {
                t.Error(err)
            }
        }(n)
    }
    wg.Wait()
    refs, err := store.Runs(reference(0).ProjectID)
    if err != nil || len(refs) != 11 {
        t.Fatal(len(refs), err)
    }
    other, err := store.Runs("p_000000000000000000000002")
    if err != nil || len(other) != 0 {
        t.Fatal(other, err)
    }

}

func TestExactRunReadsPreparationWithoutPromotingOrCrossingProjects(t *testing.T) {
    store := testStore(t)
    ref := reference(1)
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    found, err := store.Run(ref.ProjectID, ref.ID)
    if err != nil || found.ID != ref.ID || found.Submission != "preparing" || found.AttemptedAt != nil {
        t.Fatal(found, err)
    }
    listed, err := store.Runs(ref.ProjectID)
    if err != nil || len(listed) != 0 {
        t.Fatal("preparation became latest-eligible", listed, err)
    }
    for _, pair := range [][2]string{{"p_000000000000000000000002", ref.ID}, {ref.ProjectID, "r_ffffffffffffffffffffffff"}} {
        if _, err := store.Run(pair[0], pair[1]); err == nil {
            t.Fatal("found another project or missing identity", pair)
        }
    }
}

func TestCleanupMetadataSurvivesStaleSavesAndKeepsLatest(t *testing.T) {
    store := testStore(t)
    ref := reference(1)
    ref.Submission = "submitted"
    ref.AttemptedAt = &ref.CreatedAt
    stale := ref
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    completed := ref.CreatedAt.Add(time.Second)
    cleanup := &task.RunCleanup{RequestedAt: completed.Add(time.Second), Summary: wire.RunSummary{Status: "failed", CreatedAt: ref.CreatedAt, CompletedAt: &completed, ExitCode: new(7)}}
    if err := store.RecordCleanup(context.Background(), ref, cleanup); err != nil {
        t.Fatal(err)
    }
    ref.Cleanup = cleanup
    later := *cleanup
    later.RequestedAt = cleanup.RequestedAt.Add(time.Second)
    // A second concurrent attempt cannot replace the pending attempt identity.
    if err := store.RecordCleanup(context.Background(), ref, &later); err != nil {
        t.Fatal(err)
    }
    wrongAttempt := ref
    wrongAttempt.Cleanup = &later
    if err := store.RecordCleanup(context.Background(), wrongAttempt, nil); err != nil {
        t.Fatal(err)
    }
    refs, _ := store.Runs(ref.ProjectID)
    if len(refs) != 1 || refs[0].Cleanup == nil || !refs[0].Cleanup.RequestedAt.Equal(cleanup.RequestedAt) {
        t.Fatal(refs)
    }
    cleaned := completed.Add(2 * time.Second)
    confirmed := *cleanup
    confirmed.CleanedAt = &cleaned
    if err := store.RecordCleanup(context.Background(), ref, &confirmed); err != nil {
        t.Fatal(err)
    }
    var wg sync.WaitGroup
    for n := 0; n < 8; n++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            copy := stale
            copy.Submission = "preparing"
            copy.AttemptedAt = nil
            if err := store.SaveRun(context.Background(), copy); err != nil {
                t.Error(err)
            }
        }()
    }
    wg.Wait()
    if err := store.RecordCleanup(context.Background(), ref, nil); err != nil {
        t.Fatal(err)
    }
    refs, err := store.Runs(ref.ProjectID)
    if err != nil || len(refs) != 1 || refs[0].Cleanup == nil || refs[0].Cleanup.CleanedAt == nil || refs[0].Cleanup.Summary.ExitCode == nil || *refs[0].Cleanup.Summary.ExitCode != 7 || refs[0].Submission != "submitted" {
        t.Fatal(refs, err)
    }
}

func TestCleanupMetadataRejectsUnknownOutcome(t *testing.T) {
    store := testStore(t)
    ref := reference(2)
    ref.Submission, ref.AttemptedAt = "submitted", &ref.CreatedAt
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    bad := &task.RunCleanup{RequestedAt: time.Now(), Summary: wire.RunSummary{Status: "unknown"}}
    if err := store.RecordCleanup(context.Background(), ref, bad); err == nil {
        t.Fatal("retained an unconfirmed outcome")
    }

}

func TestAcceptedSubmissionCannotRegressOrChangeBinding(t *testing.T) {
    store := testStore(t)
    ref := reference(7)
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    ref.Submission = "submitted"
    ref.AttemptedAt = &ref.CreatedAt
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    conflict := ref
    conflict.RemoteDir = "/different/project"
    if err := store.SaveRun(context.Background(), conflict); err == nil {
        t.Fatal("same ID changed binding")
    }
    ref.Submission = "uncertain"
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    refs, err := store.Runs(ref.ProjectID)
    if err != nil || len(refs) != 1 || refs[0].Submission != "submitted" {
        t.Fatal(refs, err)
    }
}

func TestRunLocatorRejectsDamagedRow(t *testing.T) {
    store := testStore(t)
    ctx := context.Background()
    ref := reference(8)
    stamp := time.Date(2026, 10, 27, 12, 34, 56, 123456789, time.FixedZone("local", 8*60*60))
    ref.Version = "older-software"
    ref.Submission, ref.AttemptedAt = "submitted", &stamp
    if err := store.SaveRun(ctx, ref); err != nil {
        t.Fatal(err)
    }
    refs, err := store.Runs(ref.ProjectID)
    if err != nil || len(refs) != 1 || refs[0].Version != ref.Version || !refs[0].AttemptedAt.Equal(stamp) {
        t.Fatal(refs, err)
    }
    database, err := sql.Open("sqlite", store.Path)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    damaged := ref
    damaged.AttemptedAt = nil
    data, err := json.Marshal(damaged)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := database.Exec("UPDATE runs SET data=? WHERE id=?", data, ref.ID); err != nil {
        t.Fatal(err)
    }
    _, err = store.Runs(ref.ProjectID)
    var stateFault *fault.Error
    if !errors.As(err, &stateFault) || stateFault.Code != "BAD_CONFIG" || stateFault.Message != "run_state" {
        t.Fatal("returned damaged locator", err)
    }
    if err := store.SaveRun(ctx, ref); !errors.As(err, &stateFault) || stateFault.Code != "BAD_CONFIG" {
        t.Fatal("overwrote damaged locator", err)
    }
    var retained []byte
    if err := database.QueryRow("SELECT data FROM runs WHERE id=?", ref.ID).Scan(&retained); err != nil || string(retained) != string(data) {
        t.Fatal("changed damaged state", err)
    }
    data, err = json.Marshal(ref)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := database.Exec("UPDATE runs SET data=?,attempted_at=? WHERE id=?", data, stamp.Add(time.Second).UTC().Format(time.RFC3339Nano), ref.ID); err != nil {
        t.Fatal(err)
    }
    if _, err := store.Runs(ref.ProjectID); !errors.As(err, &stateFault) || stateFault.Code != "BAD_CONFIG" {
        t.Fatal("accepted mismatched indexed attempt", err)
    }
}
