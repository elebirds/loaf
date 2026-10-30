package client

import (
    "context"
    "os"
    "slices"
    "testing"
)

func TestUXHQueuePreviewKeepsNotificationNamesOffline(t *testing.T) {
    q, remote, _, _, _, _ := queueClientFixture(t)
    q.Remote, q.Notify, q.Projects.Runs = nil, nil, nil
    args := []string{"printf", "space here", "$(literal)", ""}
    input := QueueSubmitInput{DryRun: true, Args: args, Name: "trial", Tags: []string{"seed 1", "baseline", "seed 1"}, Notify: []string{"explicit-destination"}}
    result, err := q.Submit(context.Background(), input, nil)
    if err != nil || result.Preview == nil || result.Request != nil || len(*remote.order) != 0 {
        t.Fatal(result, err)
    }
    if !slices.Equal(result.Preview.Args, args) || !slices.Equal(result.Preview.Notifications, input.Notify) || !slices.Equal(result.Preview.Metadata.Tags, []string{"baseline", "seed 1"}) {
        t.Fatal(result.Preview)
    }
    input.Notify[0] = "changed"
    if result.Preview.Notifications[0] != "explicit-destination" {
        t.Fatal("preview aliased mutable input")
    }
    if _, err := os.Stat(q.Projects.State.Path); !os.IsNotExist(err) {
        t.Fatal("preview wrote state", err)
    }
}

func TestUXHQueueStaticAliasesShareOneLedger(t *testing.T) {
    q, remote, _, _, _, _ := queueClientFixture(t)
    ctx := context.Background()
    q.Projects.Settings.Targets["same-machine"] = q.Projects.Settings.Targets["lab"]
    submitted, err := q.Submit(ctx, QueueSubmitInput{}, nil)
    if err != nil {
        t.Fatal(err)
    }
    a, err := q.List(ctx, QueueListInput{Target: new("lab"), Limit: 1})
    if err != nil {
        t.Fatal(err)
    }
    b, err := q.List(ctx, QueueListInput{Target: new("same-machine"), Limit: 1})
    if err != nil || a.Status.TargetID != b.Status.TargetID || a.Requests[0].ID != submitted.Request.ID || b.Requests[0].ID != submitted.Request.ID {
        t.Fatal(a, b, err)
    }
    if remote.targets[len(remote.targets)-1] != remote.targets[len(remote.targets)-2] {
        t.Fatal("aliases queried different machine bindings")
    }
}
