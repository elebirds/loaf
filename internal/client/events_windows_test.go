package client

import (
    "bytes"
    "context"
    "errors"
    "io"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/sdk/agentdelivery"
)

func TestWindowsAgentAdaptersReportUnsupportedBeforeStateWrites(t *testing.T) {
    _, notifications := NewEventServices(EventDependencies{CurrentCodex: agentdelivery.Destination{Kind: "codex-desktop", Endpoint: "/private/current.sock", SessionID: "fixture-thread"}})
    service := notifications.(*eventServices)
    _, err := service.BindCurrent(context.Background(), BindCurrentInput{Name: "current"})
    var problem *fault.Error
    if !errors.As(err, &problem) || problem.Code != "DESTINATION_UNSUPPORTED" {
        t.Fatal(err)
    }
    err = service.Channel(context.Background(), ChannelInput{Socket: "/private/claude.sock", SessionID: "fixture-session"}, Streams{In: io.NopCloser(strings.NewReader("")), Out: &bytes.Buffer{}})
    if !errors.As(err, &problem) || problem.Code != "DESTINATION_UNSUPPORTED" {
        t.Fatal(err)
    }
}
