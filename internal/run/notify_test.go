package run

import (
    "context"
    "errors"
    "testing"

    "github.com/elebirds/loaf/internal/task"
)

func TestNotificationHookAfterIdentityBeforeTransferAndLaunch(t *testing.T) {
    service, input, sender, remote := executionFixture(t)
    wanted := errors.New("durable waiting relation failed")
    called := false
    input.BeforeSync = func(ctx context.Context, ref task.RunRef) error {
        called = true
        saved, err := service.Index.Run(input.Project.ID, ref.ID)
        if err != nil || saved.ID != ref.ID || saved.Submission != "preparing" || sender.calls != 0 || remote.launches != 0 {
            t.Fatal(saved, err, sender.calls, remote.launches)
        }
        return wanted
    }
    result, err := service.Submit(context.Background(), input, nil)
    if !errors.Is(err, wanted) || !called || result.ID == "" || result.Submission != "preparing" || result.StateError != "NOTIFY_FAILED" || sender.calls != 0 || remote.launches != 0 {
        t.Fatal(result, err, sender, remote)
    }
}
