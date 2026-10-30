package local

import (
    "context"
    "github.com/elebirds/loaf/internal/testutil"
    "os"
    "reflect"
    "slices"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/task"
)

func TestEqualAttemptTimesHaveStableOrder(t *testing.T) {
    store := testStore(t)
    stamp := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC)
    for n := 11; n >= 0; n-- {
        ref := reference(n)
        ref.Submission = "submitted"
        ref.AttemptedAt = &stamp
        if err := store.SaveRun(context.Background(), ref); err != nil {
            t.Fatal(err)
        }
    }
    for range 32 {
        refs, err := store.Runs(reference(0).ProjectID)
        if err != nil || len(refs) != 12 {
            t.Fatal(refs, err)
        }
        for n, ref := range refs {
            if ref.ID != reference(n).ID {
                t.Fatal("unstable tie order", refs)
            }
        }
    }
}

func TestRunMetadataIsSeparatePrivateImmutableAndConcurrent(t *testing.T) {
    store := testStore(t)
    ref := reference(1)
    if _, err := store.RunMetadata(ref.ProjectID, ref.ID); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(store.Path); !os.IsNotExist(err) {
        t.Fatal("read wrote state", err)
    }
    ref.Submission = "uncertain"
    ref.AttemptedAt = &ref.CreatedAt
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    original, err := store.Runs(ref.ProjectID)
    if err != nil {
        t.Fatal(err)
    }
    m := task.RunMetadata{Name: "中文 A/B", Tags: []string{"seed1", "ablation", "seed1"}}
    var wg sync.WaitGroup
    for range 8 {
        wg.Add(1)
        go func() {
            defer wg.Done()
            if err := store.SaveRunMetadata(context.Background(), ref.ProjectID, ref.ID, m); err != nil {
                t.Error(err)
            }
        }()
    }
    wg.Wait()
    got, err := store.RunMetadata(ref.ProjectID, ref.ID)
    if err != nil || got.Name != m.Name || !slices.Equal(got.Tags, []string{"ablation", "seed1"}) {
        t.Fatal(got, err)
    }
    testutil.Private(t, store.Path, false)
    after, _ := store.Runs(ref.ProjectID)
    if !reflect.DeepEqual(after, original) {
        t.Fatal("annotations changed locator")
    }
    if err := store.SaveRunMetadata(context.Background(), ref.ProjectID, ref.ID, task.RunMetadata{Name: "replacement"}); err == nil {
        t.Fatal("replaced immutable labels")
    }
    if _, err := store.Runs(ref.ProjectID); err != nil {
        t.Fatal("metadata corrupted locator access", err)
    }
}
