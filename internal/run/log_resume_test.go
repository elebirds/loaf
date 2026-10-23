package run

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

type cursorRemote struct {
    localRemote
    calls    int
    resumes  []string
    terminal bool
    changed  bool
}

func testCursor(req wire.Request, offset int64) string {
    token, _ := wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: req.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, FileID: "1:2", Offset: offset, Boundary: strings.Repeat("a", 64)})
    return token
}
func (r *cursorRemote) Follow(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool, emit func(wire.LogEvent) error) error {
    r.calls++
    r.resumes = append(r.resumes, req.Resume)
    if r.changed {
        return &ssh.RemoteError{Code: "LOG_CHANGED"}
    }
    record := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123}
    if r.calls == 1 {
        if err := emit(wire.LogEvent{Type: "chunk", Record: record, Log: wire.LogChunk{Text: "one\n", Offset: 4, Cursor: testCursor(req, 4)}}); err != nil {
            return err
        }
        if r.terminal {
            if err := emit(wire.LogEvent{Type: "end", Record: record, Log: wire.LogChunk{Offset: 4, Cursor: testCursor(req, 4)}}); err != nil {
                return err
            }
        }
        return fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Connection closed by fixture"})
    }
    expected, _ := wire.DecodeLogCursor(req.Resume)
    if expected.Offset != 4 {
        return errors.New("wrong resume position")
    }
    record.Status = "succeeded"
    if err := emit(wire.LogEvent{Type: "chunk", Record: record, Log: wire.LogChunk{Text: "two\n", Offset: 8, Cursor: testCursor(req, 8)}}); err != nil {
        return err
    }
    return emit(wire.LogEvent{Type: "end", Record: record, Log: wire.LogChunk{Offset: 8, Cursor: testCursor(req, 8)}})
}

func TestFollowResumeUsesOnlyConfirmedCursorAndNeverReplaysLaunch(t *testing.T) {
    for _, mode := range []string{"recover", "plain", "consumer-error", "cancel", "terminal", "changed"} {
        t.Run(mode, func(t *testing.T) {
            service, input, _, original := executionFixture(t)
            submitted, err := service.Submit(context.Background(), input, nil)
            if err != nil {
                t.Fatal(err)
            }
            remote := &cursorRemote{terminal: mode == "terminal", changed: mode == "changed"}
            service.Remote = remote
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()
            text, ends := "", 0
            emit := func(v View, kind string) error {
                if mode == "consumer-error" {
                    return errors.New("sink failed")
                }
                if mode == "cancel" {
                    cancel()
                }
                if kind == "end" {
                    ends++
                }
                text += v.Log.Text
                return nil
            }
            var last View
            if mode == "plain" {
                last, err = service.Follow(ctx, submitted.Ref, 0, 100, true, emit)
            } else {
                last, err = service.FollowResumable(ctx, submitted.Ref, 0, 100, true, "", emit)
            }
            if mode == "recover" {
                if err != nil || remote.calls != 2 || text != "one\ntwo\n" || ends != 1 || last.Status != "succeeded" || remote.resumes[0] != "" || remote.resumes[1] == "" {
                    t.Fatal(last, err, remote.calls, text, ends)
                }
            } else if err == nil || remote.calls != 1 {
                t.Fatal(mode, err, remote.calls)
            }
            if original.launches != 1 || remote.launches != 0 {
                t.Fatal("replayed execution")
            }
        })
    }
}

func TestResumeCursorIsValidatedBeforeConnecting(t *testing.T) {
    service, input, _, _ := executionFixture(t)
    submitted, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    service.Runner = func(context.Context, config.Target, string, bool) (string, error) {
        t.Fatal("invalid cursor deployed helper")
        return "", nil
    }
    req := request(submitted.Ref, 0, 100)
    req.RunID = "different"
    for _, token := range []string{"invalid", testCursor(req, 4)} {
        _, err := service.FollowResumable(context.Background(), submitted.Ref, 0, 100, true, token, nil)
        var f *fault.Error
        if !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
            t.Fatal(err)
        }
    }
}
