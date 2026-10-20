package config

import (
    "context"
    "fmt"
    "os"
    "sync"
    "testing"
    "time"
)

func reference(n int) RunRef {
    now := time.Now().UTC()
    return RunRef{ID: fmt.Sprintf("r_%024x", n), ProjectID: "p_000000000000000000000001", Target: "lab", Connection: Target{Destination: "lab", RemoteBase: "~/projects"}, RemoteDir: "/tmp/project", Runner: "/tmp/runner", Version: "dev", CreatedAt: now, Submission: "preparing"}
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
    original := `{"format_version":99,"runs":{}}`
    if err := os.WriteFile(store.runsFile(), []byte(original), 0600); err != nil {
        t.Fatal(err)
    }
    if store.SaveRun(context.Background(), reference(99)) == nil {
        t.Fatal("overwrote unknown format")
    }
    data, _ := os.ReadFile(store.runsFile())
    if string(data) != original {
        t.Fatal(string(data))
    }
}
