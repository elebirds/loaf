package client

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type checkpointRemote struct {
    execution.Remote
    calls int
}

func (r *checkpointRemote) Follow(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool, emit func(wire.LogEvent) error) error {
    r.calls++
    for i, text := range []string{"one\n", "two\n", ""} {
        offset := int64(min(i+1, 2) * 4)
        token, err := wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: req.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, FileID: "1:2", Offset: offset, Boundary: strings.Repeat("a", 64)})
        if err != nil {
            return err
        }
        kind := "chunk"
        record := &wire.RunRecord{FormatVersion: wire.FormatVersion, Request: req, Status: "running", SupervisorPID: 123}
        if i == 2 {
            kind, record.Status = "end", "succeeded"
        }
        if err := emit(wire.LogEvent{Type: kind, Record: record, Log: wire.LogChunk{Text: text, Offset: offset, Cursor: token}}); err != nil {
            return err
        }
    }
    return nil
}

func TestLogsCheckpointRequiresSuccessfulConsumption(t *testing.T) {
    for _, mode := range []string{"acknowledged", "unacknowledged", "consumer-error", "checkpoint-error", "checkpoint-validation"} {
        t.Run(mode, func(t *testing.T) {
            ctx := context.Background()
            root, _, err := config.Discover(ctx, t.TempDir())
            if err != nil {
                t.Fatal(err)
            }
            state := local.New(t.TempDir())
            project, err := state.Select(ctx, root, "lab")
            if err != nil {
                t.Fatal(err)
            }
            now := time.Now().UTC()
            ref := task.RunRef{ID: "r_000000000000000000000001", ProjectID: project.ID, Target: "lab", RemoteDir: "/remote/project", Runner: "/remote/runner", Version: wire.Version, CreatedAt: now, AttemptedAt: &now, Submission: "submitted"}
            if err := state.SaveRun(ctx, ref); err != nil {
                t.Fatal(err)
            }
            remote := &checkpointRemote{}
            projects := Projects{CWD: root, State: state, Runs: &execution.Service{Index: state, Remote: remote, Observations: state}}
            checkpoint := filepath.Join(t.TempDir(), "cursor.json")
            consumed := 0
            sinkError := errors.New("consumer failed")
            result, err := projects.Logs(ctx, LogsInput{ID: ref.ID, Follow: true, ResumeFile: &checkpoint}, LogsFeedback{Emit: func(view execution.View, kind string) (bool, error) {
                consumed++
                if consumed == 1 {
                    if _, err := os.Stat(checkpoint); !errors.Is(err, os.ErrNotExist) {
                        t.Fatalf("checkpoint preceded consumption: %v", err)
                    }
                }
                if consumed == 2 {
                    switch mode {
                    case "consumer-error":
                        return true, sinkError
                    case "checkpoint-error":
                        if err := os.Remove(checkpoint + ".lock"); err != nil {
                            t.Fatal(err)
                        }
                        if err := os.Mkdir(checkpoint+".lock", 0700); err != nil {
                            t.Fatal(err)
                        }
                    case "checkpoint-validation":
                        // Another consumer has acknowledged a later valid cursor.
                        // This stream cannot rewind it while saving this chunk.
                        cursor, err := wire.DecodeLogCursor(view.Log.Cursor)
                        if err != nil {
                            t.Fatal(err)
                        }
                        cursor.Offset = 12
                        ahead, err := wire.EncodeLogCursor(cursor)
                        if err != nil {
                            t.Fatal(err)
                        }
                        if err := local.SaveLogCheckpoint(ctx, checkpoint, ahead); err != nil {
                            t.Fatal(err)
                        }
                    }
                }
                return mode != "unacknowledged", nil
            }})
            failed := mode == "consumer-error" || mode == "checkpoint-error" || mode == "checkpoint-validation"
            wantConsumed := 3
            if failed {
                wantConsumed = 2
            }
            if (err != nil) != failed || remote.calls != 1 || consumed != wantConsumed {
                t.Fatalf("observation: consumed=%d connections=%d err=%v", consumed, remote.calls, err)
            }
            if mode == "consumer-error" && !errors.Is(err, sinkError) {
                t.Fatalf("lost consumer error: %v", err)
            }
            saved, saveErr := state.RunObservation(project.ID, ref.ID)
            if saveErr != nil || saved.Last == nil || saved.Failure != nil || result.View.QueryError != "" {
                t.Fatal("consumer/checkpoint errors became query failures", result, saved, saveErr)
            }
            if failed && (result.View.Log == nil || result.View.Log.Offset != 4 || result.View.ObservedAt.IsZero()) {
                t.Fatal("failed consumption advanced the returned cursor", result)
            }
            if (result.View.StateError == "LOG_CHECKPOINT_FAILED") != (mode == "checkpoint-error") {
                t.Fatal("checkpoint persistence error was not separate", result)
            }
            if mode == "checkpoint-validation" {
                var known *fault.Error
                if !errors.As(err, &known) || known.Code != "BAD_CONFIG" || result.View.StateError != "BAD_CONFIG" {
                    t.Fatal("checkpoint validation lost its stable error", result, err)
                }
            }
            token, err := local.ReadLogCheckpoint(checkpoint)
            if err != nil {
                t.Fatal(err)
            }
            if mode == "unacknowledged" {
                if token != "" {
                    t.Fatal("unacknowledged consumption advanced checkpoint")
                }
                return
            }
            cursor, err := wire.DecodeLogCursor(token)
            want := int64(8)
            if failed {
                want = 4
            }
            if mode == "checkpoint-validation" {
                want = 12
            }
            if err != nil || cursor.Offset != want {
                t.Fatalf("checkpoint offset: got=%d want=%d err=%v", cursor.Offset, want, err)
            }
        })
    }
}

func TestOfflineLogAcknowledgementFailureRetainsIdentityAndCheckpoint(t *testing.T) {
    for _, mode := range []string{"io", "rewind"} {
        t.Run(mode, func(t *testing.T) {
            ctx := context.Background()
            root, _, err := config.Discover(ctx, t.TempDir())
            if err != nil {
                t.Fatal(err)
            }
            state := local.New(t.TempDir())
            project, err := state.Select(ctx, root, "lab")
            if err != nil {
                t.Fatal(err)
            }
            now := time.Now().UTC()
            ref := task.RunRef{ID: "r_000000000000000000000001", ProjectID: project.ID, Target: "lab", RemoteDir: "/remote/project", Runner: "/remote/runner", Version: wire.Version, CreatedAt: now, AttemptedAt: &now, Submission: "submitted"}
            if err := state.SaveRun(ctx, ref); err != nil {
                t.Fatal(err)
            }
            token := func(offset int64) string {
                value, err := wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: ref.Version, Root: ref.RemoteDir, ProjectID: ref.ProjectID, RunID: ref.ID, FileID: "1:2", Offset: offset, Boundary: strings.Repeat("a", 64)})
                if err != nil {
                    t.Fatal(err)
                }
                return value
            }
            checkpoint := filepath.Join(t.TempDir(), "cursor.json")
            previous := token(4)
            if err := local.SaveLogCheckpoint(ctx, checkpoint, previous); err != nil {
                t.Fatal(err)
            }
            next, want := token(8), "LOG_CHECKPOINT_FAILED"
            if mode == "rewind" {
                next, want = token(0), "BAD_CONFIG"
            } else {
                if err := os.Remove(checkpoint + ".lock"); err != nil {
                    t.Fatal(err)
                }
                if err := os.Mkdir(checkpoint+".lock", 0700); err != nil {
                    t.Fatal(err)
                }
            }
            remote := &checkpointRemote{}
            projects := Projects{CWD: root, State: state, Runs: &execution.Service{Index: state, Remote: remote}}
            result, err := projects.Logs(ctx, LogsInput{ID: ref.ID, ResumeFile: &checkpoint, AckCursor: &next}, LogsFeedback{})
            var known *fault.Error
            if !errors.As(err, &known) || known.Code != want || result.View.ID != ref.ID || result.Acknowledged || result.View.StateError != want || result.View.QueryError != "" || remote.calls != 0 {
                t.Fatal(result, err, remote.calls)
            }
            if after, err := local.ReadLogCheckpoint(checkpoint); err != nil || after != previous {
                t.Fatal("failed acknowledgement changed the checkpoint", after, err)
            }
        })
    }
}

func TestCheckpointErrorPreservesValidationAndCancellation(t *testing.T) {
    invalid := fault.New("BAD_CONFIG", "log_checkpoint_invalid", nil)
    code, err := checkpointError(invalid)
    if code != "BAD_CONFIG" || err != invalid {
        t.Fatal(code, err)
    }
    for _, canceled := range []error{context.Canceled, context.DeadlineExceeded} {
        code, err := checkpointError(canceled)
        if code != "OPERATION_CANCELED" || !errors.Is(err, canceled) {
            t.Fatal(code, err)
        }
    }
}
