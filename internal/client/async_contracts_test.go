package client

import (
    "encoding/json"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
)

func TestAsyncPublicContractsKeepPrivateBindingsSeparate(t *testing.T) {
    values := []any{
        DestinationRecord{Name: "research", Kind: "codex", SessionID: "thread_1"},
        Subscription{Subject: event.Subject{Kind: "resource_operation", ID: "c_1"}},
        QueueSubmitResult{Request: &queue.Request{ID: "q_1", RunID: "r_1"}},
    }
    for _, v := range values {
        b, err := json.Marshal(v)
        if err != nil {
            t.Fatal(err)
        }
        for _, private := range []string{"endpoint", "credential", "env", "command", "ssh_args"} {
            if strings.Contains(string(b), private) {
                t.Fatalf("private %s in %s", private, b)
            }
        }
    }
}

func TestDirectServicesDoNotRequireAsyncDependencies(t *testing.T) {
    app := New(Dependencies{}, config.Global{})
    if app.Projects == nil || app.Machines == nil || app.Resources == nil {
        t.Fatal("direct service composition depends on async components")
    }
    if app.Events != nil || app.Queue != nil || app.Notify != nil {
        t.Fatal("async components enabled implicitly")
    }
}
