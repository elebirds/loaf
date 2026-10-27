package local

import (
    "context"
    "reflect"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/task"
)

func TestObservationConcurrentMerge(t *testing.T) {
    s := testStore(t)
    ref := reference(1)
    ref.Submission = "submitted"
    now := time.Now().UTC()
    ref.AttemptedAt = &now
    if err := s.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    before, _ := s.Runs(ref.ProjectID)
    var wg sync.WaitGroup
    for i := range 12 {
        wg.Add(1)
        go func() {
            defer wg.Done()
            stamp := now.Add(time.Duration(i) * time.Second)
            if err := s.SaveRunObservation(context.Background(), ref.ProjectID, ref.ID, task.RunObservation{Last: &task.ObservedRun{ObservedAt: stamp, Status: "running"}}); err != nil {
                t.Error(err)
            }
        }()
    }
    wg.Wait()
    got, err := s.RunObservation(ref.ProjectID, ref.ID)
    if err != nil || !got.Last.ObservedAt.Equal(now.Add(11*time.Second)) {
        t.Fatal(got, err)
    }
    after, _ := s.Runs(ref.ProjectID)
    if !reflect.DeepEqual(before, after) {
        t.Fatal("changed locator")
    }
}

func TestTerminalObservationAndIndependentFailureSurviveLateReplies(t *testing.T) {
    s := testStore(t)
    ref := reference(2)
    ref.Submission = "submitted"
    ref.AttemptedAt = &ref.CreatedAt
    if err := s.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    stamp := ref.CreatedAt.Add(time.Second)
    terminal := &task.ObservedRun{ObservedAt: stamp, Status: "failed", ExitCode: new(7), CompletedAt: &stamp}
    if err := s.SaveRunObservation(context.Background(), ref.ProjectID, ref.ID, task.RunObservation{Last: terminal}); err != nil {
        t.Fatal(err)
    }
    for _, late := range []*task.ObservedRun{
        {ObservedAt: stamp.Add(time.Second), Status: "running"},
        {ObservedAt: stamp.Add(time.Second), Status: "failed", ExitCode: new(9), CompletedAt: &stamp},
        {ObservedAt: stamp.Add(time.Second), Status: "failed", ExitCode: new(7), Signal: "killed", CompletedAt: &stamp},
    } {
        if err := s.SaveRunObservation(context.Background(), ref.ProjectID, ref.ID, task.RunObservation{Last: late}); err != nil {
            t.Fatal(err)
        }
    }
    failure := &task.QueryFailure{ObservedAt: stamp.Add(2 * time.Second), Code: "SSH_FAILED", Stage: "observe"}
    if err := s.SaveRunObservation(context.Background(), ref.ProjectID, ref.ID, task.RunObservation{Failure: failure}); err != nil {
        t.Fatal(err)
    }
    got, err := s.RunObservation(ref.ProjectID, ref.ID)
    if err != nil || !reflect.DeepEqual(got.Last, terminal) || !reflect.DeepEqual(got.Failure, failure) {
        t.Fatal(got, err)
    }
}
