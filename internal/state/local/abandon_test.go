package local

import (
    "context"
    "github.com/elebirds/loaf/internal/task"
    "testing"
    "time"
)

func TestAbandonmentSurvivesLateSubmissionAndRejectedRetry(t *testing.T) {
    ctx := context.Background()
    store := testStore(t)
    ref := reference(21)
    if err := store.SaveRun(ctx, ref); err != nil {
        t.Fatal(err)
    }
    pending := &task.RunAbandonment{RequestedAt: time.Now().UTC()}
    if err := store.RecordAbandonment(ctx, ref, pending); err != nil {
        t.Fatal(err)
    }
    stale := ref
    stale.Submission, stale.AttemptedAt = "uncertain", &ref.CreatedAt
    if err := store.SaveRun(ctx, stale); err != nil {
        t.Fatal(err)
    }
    got, err := store.Run(ref.ProjectID, ref.ID)
    if err != nil || got.Submission != "uncertain" || got.Abandonment == nil {
        t.Fatal(got, err)
    }
    now := time.Now().UTC()
    proof := &task.RunAbandonment{RequestedAt: pending.RequestedAt, NoStartAt: &now}
    if err := store.RecordAbandonment(ctx, ref, proof); err != nil {
        t.Fatal(err)
    }
    stale.Submission = "submitted"
    if err := store.SaveRun(ctx, stale); err != nil {
        t.Fatal(err)
    }
    ref.Abandonment = pending
    if err := store.RecordAbandonment(ctx, ref, nil); err != nil {
        t.Fatal(err)
    }
    proof.CleanedAt = &now
    if err := store.RecordAbandonment(ctx, ref, proof); err != nil {
        t.Fatal(err)
    }
    if err := store.RecordAbandonment(ctx, ref, pending); err != nil {
        t.Fatal(err)
    }
    got, err = store.Run(ref.ProjectID, ref.ID)
    if err != nil || got.Abandonment == nil || got.Abandonment.NoStartAt == nil || got.Abandonment.CleanedAt == nil || got.Submission != "uncertain" {
        t.Fatal(got, err)
    }
    listed, err := store.Runs(ref.ProjectID)
    if err != nil || len(listed) != 1 || listed[0].ID != ref.ID {
        t.Fatal("uncertain latest binding lost", listed, err)
    }
}

func TestConfirmedLaunchWinsUnprovenAbandonmentIntent(t *testing.T) {
    ctx := context.Background()
    store := testStore(t)
    ref := reference(23)
    if err := store.SaveRun(ctx, ref); err != nil {
        t.Fatal(err)
    }
    pending := &task.RunAbandonment{RequestedAt: time.Now().UTC()}
    if err := store.RecordAbandonment(ctx, ref, pending); err != nil {
        t.Fatal(err)
    }
    accepted := ref
    accepted.Submission, accepted.AttemptedAt = "submitted", &ref.CreatedAt
    if err := store.SaveRun(ctx, accepted); err != nil {
        t.Fatal(err)
    }
    got, err := store.Run(ref.ProjectID, ref.ID)
    if err != nil || got.Submission != "submitted" || got.Abandonment != nil {
        t.Fatal(got, err)
    }
    ref.Abandonment = pending
    if err := store.RecordAbandonment(ctx, ref, nil); err != nil {
        t.Fatal(err)
    }
    got, err = store.Run(ref.ProjectID, ref.ID)
    if err != nil || got.Submission != "submitted" || got.Abandonment != nil {
        t.Fatal(got, err)
    }
}

func TestAbandonmentCannotRetireSubmittedOrQueuedRun(t *testing.T) {
    for _, kind := range []string{"submitted", "queue"} {
        t.Run(kind, func(t *testing.T) {
            store := testStore(t)
            ref := reference(22)
            if kind == "submitted" {
                ref.Submission, ref.AttemptedAt = "submitted", &ref.CreatedAt
            } else {
                ref.QueueRequestID = "q_000000000000000000000001"
            }
            if err := store.SaveRun(context.Background(), ref); err != nil {
                t.Fatal(err)
            }
            if err := store.RecordAbandonment(context.Background(), ref, &task.RunAbandonment{RequestedAt: time.Now().UTC()}); err == nil {
                t.Fatal("retired accepted/queue run")
            }
        })
    }
}
