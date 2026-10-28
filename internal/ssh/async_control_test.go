package ssh

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/queue"
    "github.com/elebirds/loaf/internal/wire"
)

func TestAsyncMutationIsNeverClassifiedAsRead(t *testing.T) {
    for _, action := range []string{"enable", "prepare", "enqueue", "cancel", "pause", "clean", "unknown"} {
        if readOnlyControl(wire.Request{Action: "queue", Queue: &queue.Command{Action: action}}) {
            t.Fatalf("mutation %s may be replayed", action)
        }
    }
    for _, action := range []string{"list", "show", "status", "events", "clean-preview"} {
        if !readOnlyControl(wire.Request{Action: "queue", Queue: &queue.Command{Action: action}}) {
            t.Fatalf("read %s cannot use bounded recovery", action)
        }
    }
    if readOnlyControl(wire.Request{Action: "queue"}) {
        t.Fatal("missing queue control classified as read")
    }
}

func TestControlResponseRetainsUnconfirmedQueueIdentity(t *testing.T) {
    response := wire.Response{FormatVersion: wire.FormatVersion, Version: wire.Version, OK: false, Queue: &queue.Result{Request: &queue.Request{ID: "q_1", RunID: "r_1", State: "unconfirmed"}}, Error: &wire.Error{Code: "QUEUE_DISPATCH_UNCONFIRMED"}}
    data, err := json.Marshal(response)
    if err != nil {
        t.Fatal(err)
    }
    fake := filepath.Join(t.TempDir(), "ssh")
    script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf '%%s' %s\nexit 1\n", Quote(string(data)))
    if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
        t.Fatal(err)
    }
    client := &Client{Binary: fake}
    got, err := client.ControlResponse(context.Background(), config.Target{Destination: "fixture"}, "/runner", wire.Request{Version: wire.Version, Action: "queue", Queue: &queue.Command{Action: "enqueue"}}, true)
    var remote *RemoteError
    if !errors.As(err, &remote) || remote.Code != "QUEUE_DISPATCH_UNCONFIRMED" || got.Queue == nil || got.Queue.Request == nil || got.Queue.Request.ID != "q_1" || got.Queue.Request.RunID != "r_1" {
        t.Fatalf("lost partial identity: %#v %v", got, err)
    }
}

func TestControlDoesNotExposeUnverifiedResponseRecord(t *testing.T) {
    for _, response := range []wire.Response{
        {FormatVersion: 99, Version: wire.Version, OK: true, Record: &wire.RunRecord{Status: "succeeded"}},
        {FormatVersion: wire.FormatVersion, Version: "foreign", OK: true, Record: &wire.RunRecord{Status: "succeeded"}},
    } {
        data, err := json.Marshal(response)
        if err != nil {
            t.Fatal(err)
        }
        fake := filepath.Join(t.TempDir(), "ssh")
        script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf '%%s' %s\n", Quote(string(data)))
        if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
            t.Fatal(err)
        }
        record, err := (&Client{Binary: fake}).Control(context.Background(), config.Target{Destination: "fixture"}, "/runner", wire.Request{Version: wire.Version, Action: "status"}, true)
        if err == nil || record != nil {
            t.Fatalf("unverified result exposed: %#v %v", record, err)
        }
    }
}
