package config

import (
    "context"
    "encoding/json"
    "os"
    "slices"
    "strings"
    "sync"
    "testing"
    "time"
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
    if _, err := os.Stat(store.metadataFile(ref.ID)); !os.IsNotExist(err) {
        t.Fatal("read wrote state", err)
    }
    if err := store.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    original, err := os.ReadFile(store.runsFile())
    if err != nil {
        t.Fatal(err)
    }
    m := RunMetadata{Name: "中文 A/B", Tags: []string{"seed1", "ablation", "seed1"}}
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
    info, err := os.Stat(store.metadataFile(ref.ID))
    if err != nil || info.Mode().Perm() != 0600 {
        t.Fatal(info, err)
    }
    after, _ := os.ReadFile(store.runsFile())
    if string(after) != string(original) {
        t.Fatal("annotations changed legacy index")
    }
    var decoded runIndex
    d := json.NewDecoder(strings.NewReader(string(after)))
    d.DisallowUnknownFields()
    if err := d.Decode(&decoded); err != nil {
        t.Fatal(err)
    }
    if err := store.SaveRunMetadata(context.Background(), ref.ProjectID, ref.ID, RunMetadata{Name: "replacement"}); err == nil {
        t.Fatal("replaced immutable labels")
    }
    unknown := []byte(`{"format_version":99}`)
    if err := os.WriteFile(store.metadataFile(ref.ID), unknown, 0600); err != nil {
        t.Fatal(err)
    }
    if err := store.SaveRunMetadata(context.Background(), ref.ProjectID, ref.ID, m); err == nil {
        t.Fatal("overwrote unknown metadata")
    }
    saved, _ := os.ReadFile(store.metadataFile(ref.ID))
    if string(saved) != string(unknown) {
        t.Fatal(string(saved))
    }
    if _, err := store.Runs(ref.ProjectID); err != nil {
        t.Fatal("metadata corrupted locator access", err)
    }
}

func TestRunMetadataRejectsInvalidLabels(t *testing.T) {
    for _, m := range []RunMetadata{{Name: "bad\nname"}, {Name: string([]byte{255})}, {Name: strings.Repeat("界", 129)}, {Tags: []string{""}}, {Tags: []string{strings.Repeat("x", 65)}}, {Tags: make([]string, 17)}} {
        if _, err := NormalizeRunMetadata(m); err == nil {
            t.Fatal(m)
        }
    }
}
