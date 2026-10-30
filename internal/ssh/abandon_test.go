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

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/wire"
)

func TestAbandonTransportRetainsReceiptAndNeverReplaysMutation(t *testing.T) {
    now := time.Now().UTC()
    proof := &wire.AbandonReceipt{FormatVersion: 1, Version: wire.Version, Root: "/project", ProjectID: "project", RunID: "run", NoStartAt: &now}
    response := wire.Response{FormatVersion: wire.FormatVersion, Version: wire.Version, Abandonment: proof, Error: &wire.Error{Code: "ABANDON_UNCONFIRMED"}}
    data, _ := json.Marshal(response)
    count := filepath.Join(t.TempDir(), "calls")
    fake := fixtureProcess(t, processFixture{Stdout: string(data), Exit: 1, Count: count})
    client := &Client{Binary: fake}
    got, err := client.Abandon(context.Background(), config.Target{Destination: "fixture"}, "/runner", wire.Request{FormatVersion: 1, Version: wire.Version}, false, true)
    var remote *RemoteError
    if !errors.As(err, &remote) || remote.Code != "ABANDON_UNCONFIRMED" || got == nil || got.NoStartAt == nil || got.CleanedAt != nil {
        t.Fatal(got, err)
    }
    calls, err := os.ReadFile(count)
    if err != nil || strings.Count(string(calls), "call") != 1 {
        t.Fatal(string(calls), err)
    }
    if readOnlyControl(wire.Request{Action: "abandon"}) {
        t.Fatal("abandonment classified as replayable read")
    }
}

func TestAbandonTransportRejectsUnverifiedReceipt(t *testing.T) {
    for _, response := range []wire.Response{{FormatVersion: 99, Version: wire.Version}, {FormatVersion: 1, Version: "foreign"}} {
        response.Abandonment = &wire.AbandonReceipt{RunID: "run"}
        data, _ := json.Marshal(response)
        client := &Client{Binary: fixtureProcess(t, processFixture{Stdout: string(data)})}
        got, err := client.Abandon(context.Background(), config.Target{Destination: "fixture"}, "/runner", wire.Request{Version: wire.Version}, true, true)
        if err == nil || got != nil {
            t.Fatal(got, err)
        }
    }
}
