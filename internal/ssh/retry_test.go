package ssh

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

func TestReadRetriesOnlyTransientFailuresAndHonorsCancellation(t *testing.T) {
    for _, detail := range []string{"Connection closed by fixture", "Permission denied", "Host key verification failed", "No such file or directory", "REMOTE HOST IDENTIFICATION HAS CHANGED"} {
        calls := 0
        err := retryReadTrace(context.Background(), nil, func() error {
            calls++
            return fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": detail})
        })
        want := 1
        if strings.HasPrefix(detail, "Connection closed") {
            want = 2
        }
        if err == nil || calls != want {
            t.Fatal(detail, calls, err)
        }
    }
    ctx, cancel := context.WithCancel(context.Background())
    calls := 0
    started := time.Now()
    _ = retryReadTrace(ctx, nil, func() error {
        calls++
        cancel()
        return fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Connection reset"})
    })
    if calls != 1 || time.Since(started) > time.Second {
        t.Fatal("retried canceled request", calls)
    }
}

func TestReadRecoveryDoesNotIgnoreProviderRetryAfter(t *testing.T) {
    calls := 0
    err := retryReadTrace(context.Background(), nil, func() error {
        calls++
        return cloud.Failure(&cloud.APIError{Code: "TRANSPORT", HTTPStatus: 503, RetryAfter: time.Minute})
    })
    if err == nil || calls != 1 {
        t.Fatal("retried before provider's window", calls, err)
    }
}

func TestSilentReadFailureRetriesOnlyAfterItsPrivateMasterExited(t *testing.T) {
    for _, tc := range []struct {
        name           string
        scoped, exited bool
        detail         string
        want           int
    }{
        {name: "direct exit 255", want: 1},
        {name: "live master", scoped: true, want: 1},
        {name: "dead master", scoped: true, exited: true, want: 2},
        {name: "auth failure", scoped: true, exited: true, detail: "Permission denied", want: 1},
        {name: "host trust failure", scoped: true, exited: true, detail: "Host key verification failed", want: 1},
    } {
        t.Run(tc.name, func(t *testing.T) {
            session := connectionSession{}
            if tc.scoped {
                done := make(chan struct{})
                if tc.exited {
                    close(done)
                }
                session.masterDone = done
            }
            calls := 0
            err := retryReadTrace(context.Background(), nil, func() error {
                calls++
                failure := fault.Wrap("SSH_FAILED", "ssh_failed", session.transportError(errors.New("exit status 255")))
                failure.Params = map[string]any{"Detail": tc.detail}
                return failure
            })
            if err == nil || calls != tc.want {
                t.Fatal(calls, err)
            }
        })
    }
}

func TestReadRecoveryNeverReplaysMutations(t *testing.T) {
    for _, action := range []string{"version", "status", "logs", "launch", "stop"} {
        t.Run(action, func(t *testing.T) {
            count := filepath.Join(t.TempDir(), "calls")
            t.Setenv("COUNT_FILE", count)
            fake := fixtureProcess(t, processFixture{Count: count, Stderr: "Connection closed by fixture", Exit: 255})
            c := &Client{Binary: fake}
            _, err := c.Control(context.Background(), config.Target{Destination: "fixture"}, "/runner", wire.Request{Version: wire.Version, Action: action}, true)
            data, readErr := os.ReadFile(count)
            want := 1
            if action == "version" || action == "status" || action == "logs" {
                want = 2
            }
            if err == nil || readErr != nil || strings.Count(string(data), "call") != want || fault.Stage(err) == "" {
                t.Fatal(string(data), err, readErr)
            }
            var failure *fault.Error
            if !errors.As(err, &failure) || failure.Code != "SSH_FAILED" {
                t.Fatal(err)
            }
        })
    }
}

func TestDownloadRecoveryKeepsCompletedFilesAndDoesNotReplaySourceSync(t *testing.T) {
    for _, download := range []bool{true, false} {
        dir := t.TempDir()
        count := filepath.Join(dir, "attempted")
        completed := filepath.Join(dir, "completed.txt")
        t.Setenv("COUNT_FILE", count)
        t.Setenv("COMPLETED_FILE", completed)
        binary := fixtureProcess(t, processFixture{Count: count, Retry: "download", Completed: completed})
        r := Rsync{Client: &Client{Binary: "ssh", Cygpath: binary}, Binary: binary}
        target := config.Target{Destination: "fixture"}
        if download {
            result, err := r.Pull(context.Background(), target, "/runner", dir, PullInput{Root: "/root", ProjectID: "project", RunID: "run", Paths: []string{"outputs"}}, true)
            calls, readErr := os.ReadFile(count)
            if err != nil || readErr != nil || !result.Retried || result.ChangedFiles != 1 || strings.Count(string(calls), "call") != 2 {
                t.Fatal(result, err, readErr, string(calls))
            }
        } else {
            _, err := r.Sync(context.Background(), target, "/runner", dir, SyncInput{Root: "/root", ProjectID: "project", Mode: "mirror", Paths: []string{"source.py"}}, true)
            calls, readErr := os.ReadFile(count)
            if err == nil || readErr != nil || strings.Count(string(calls), "call") != 1 || fault.Stage(err) != "sync" {
                t.Fatal(err, readErr, string(calls))
            }
        }
    }
}

func TestReadRecoveryRetainsAResponseWhenReconnectReturnsNoData(t *testing.T) {
    dir := t.TempDir()
    marker := filepath.Join(dir, "attempt")
    code := 7
    response, err := json.Marshal(wire.Response{FormatVersion: wire.FormatVersion, Version: wire.Version, OK: true, Record: &wire.RunRecord{FormatVersion: wire.FormatVersion, Status: "failed", Phase: "command", ExitCode: &code}})
    if err != nil {
        t.Fatal(err)
    }
    fake := fixtureProcess(t, processFixture{Count: marker, Stdout: string(response), Stderr: "Connection closed by fixture", Exit: 255, Retry: "response"})
    client := &Client{Binary: fake}
    record, err := client.Control(context.Background(), config.Target{Destination: "fixture"}, "/runner", wire.Request{Version: wire.Version, Action: "status"}, true)
    if err == nil || record == nil || record.Status != "failed" || record.ExitCode == nil || *record.ExitCode != 7 {
        t.Fatal("erased the retained execution outcome", record, err)
    }
}
