package config

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/wire"
)

func TestObservationConcurrentMergeAndUnknownFormat(t *testing.T) {
    s := testStore(t)
    ref := reference(1)
    ref.Submission = "submitted"
    now := time.Now().UTC()
    ref.AttemptedAt = &now
    if err := s.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    before, _ := os.ReadFile(s.runsFile())
    var wg sync.WaitGroup
    for i := range 12 {
        wg.Add(1)
        go func() {
            defer wg.Done()
            stamp := now.Add(time.Duration(i) * time.Second)
            if err := s.SaveRunObservation(context.Background(), ref.ProjectID, ref.ID, RunObservation{Last: &ObservedRun{ObservedAt: stamp, Status: "running"}}); err != nil {
                t.Error(err)
            }
        }()
    }
    wg.Wait()
    got, err := s.RunObservation(ref.ProjectID, ref.ID)
    if err != nil || !got.Last.ObservedAt.Equal(now.Add(11*time.Second)) {
        t.Fatal(got, err)
    }
    after, _ := os.ReadFile(s.runsFile())
    if string(before) != string(after) {
        t.Fatal("changed locator")
    }
    unknown := []byte(`{"format_version":99}`)
    _ = os.WriteFile(s.observationFile(ref.ID), unknown, 0600)
    if err := s.SaveRunObservation(context.Background(), ref.ProjectID, ref.ID, RunObservation{}); err == nil {
        t.Fatal("unknown overwritten")
    }
    after, _ = os.ReadFile(s.observationFile(ref.ID))
    if string(after) != string(unknown) {
        t.Fatal(string(after))
    }
}
func TestCheckpointMonotonicPrivateAndRejectsUnknownOrSymlink(t *testing.T) {
    p := filepath.Join(t.TempDir(), "cursor.json")
    token := func(n int64) string {
        s, _ := wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: "v", Root: "/r", ProjectID: "p", RunID: "r", FileID: "1:2", Offset: n, Boundary: strings.Repeat("a", 64)})
        return s
    }
    if err := SaveLogCheckpoint(context.Background(), p, token(5)); err != nil {
        t.Fatal(err)
    }
    if err := SaveLogCheckpoint(context.Background(), p, token(4)); err == nil {
        t.Fatal("rewound")
    }
    if got, err := ReadLogCheckpoint(p); err != nil || got != token(5) {
        t.Fatal(got, err)
    }
    info, _ := os.Stat(p)
    if info.Mode().Perm() != 0600 {
        t.Fatal(info.Mode())
    }
    link := filepath.Join(t.TempDir(), "link")
    _ = os.Symlink(p, link)
    if err := SaveLogCheckpoint(context.Background(), link, token(6)); err == nil {
        t.Fatal("followed link")
    }
    _ = os.WriteFile(p, []byte(`{"format_version":99}`), 0600)
    if err := SaveLogCheckpoint(context.Background(), p, token(6)); err == nil {
        t.Fatal("overwrote unknown")
    }
}
