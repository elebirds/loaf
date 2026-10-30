package client

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

func TestUXDStopReviewIsOfflineExplicitAndUsesSameGrace(t *testing.T) {
    ctx := context.Background()
    root := t.TempDir()
    root, _, err := config.Discover(ctx, root)
    if err != nil {
        t.Fatal(err)
    }
    store := local.New(t.TempDir())
    project, err := store.Select(ctx, root, "replacement")
    if err != nil {
        t.Fatal(err)
    }
    now := time.Now().UTC()
    ref := task.RunRef{ID: "r_000000000000000000000001", ProjectID: project.ID, Target: "retired", Connection: config.Target{Destination: "original", RemoteBase: "~/projects"}, RemoteDir: "/tmp/original", Runner: "/tmp/runner", Version: wire.Version, CreatedAt: now, AttemptedAt: &now, Submission: "uncertain"}
    if err := store.SaveRun(ctx, ref); err != nil {
        t.Fatal(err)
    }
    c := &Projects{State: store, CWD: root}
    for _, test := range []struct {
        global, project string
        override        *string
        want            time.Duration
    }{{want: 10 * time.Second}, {global: "30s", want: 30 * time.Second}, {global: "30s", project: "2s", want: 2 * time.Second}, {global: "30s", project: "2s", override: new("0s")}} {
        c.Settings.Stop.Grace = test.global
        text := ""
        if test.project != "" {
            text = "[stop]\ngrace='" + test.project + "'\n"
        }
        if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte(text), 0600); err != nil {
            t.Fatal(err)
        }
        review, err := c.PrepareStop(ctx, StopInput{ID: ref.ID, Grace: test.override})
        if err != nil || review.Grace != test.want || review.Run.ID != ref.ID || review.Run.Target != "retired" || review.Run.Destination != "original" || review.Run.Status != "unknown" || review.Run.Submission != "uncertain" {
            t.Fatal(review, err)
        }
        _, grace, err := c.stopContext(ctx, StopInput{ID: ref.ID, Grace: test.override})
        if err != nil || grace != nil && *grace != review.Grace {
            t.Fatal("review and execution disagree", grace, review, err)
        }
    }
    for _, input := range []StopInput{{}, {ID: ref.ID, Grace: new("")}, {ID: ref.ID, Grace: new("6m")}} {
        _, err := c.PrepareStop(ctx, input)
        var f *fault.Error
        if !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
            t.Fatal(input, err)
        }
    }
    c.CWD = t.TempDir()
    if _, err := c.PrepareStop(ctx, StopInput{ID: ref.ID}); err == nil {
        t.Fatal("review crossed project boundary")
    }
}
