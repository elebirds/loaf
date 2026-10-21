package run

import (
    "context"
    "errors"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

func TestMismatchedLocatorNeverDeploysOrExecutes(t *testing.T) {
    service, ref, remote, _ := cleanupFixture(t)
    ref.Version = "different-version"
    service.Runs.Runner = func(context.Context, config.Target, string, bool) (string, error) {
        t.Fatal("deployed for a mismatched task")
        return "", nil
    }
    for _, operation := range []func() error{
        func() error { _, err := service.Runs.Observe(context.Background(), ref, 0, 0, true); return err },
        func() error { _, err := service.Runs.Follow(context.Background(), ref, 0, 0, true, nil); return err },
        func() error { _, err := service.Runs.Stop(context.Background(), ref, nil, true); return err },
        func() error { _, err := service.Preview(context.Background(), ref, true, false); return err },
        func() error { _, err := service.Clean(context.Background(), cleanResult(ref), true); return err },
    } {
        err := operation()
        var f *fault.Error
        if !errors.As(err, &f) || f.Code != "VERSION_MISMATCH" || f.Params["Expected"] != wire.Version || f.Params["Actual"] != ref.Version {
            t.Fatal(err)
        }
    }
    if remote.queries != 0 || remote.arches != 0 || remote.cleans != 0 || remote.deploys != 0 {
        t.Fatal(remote)
    }
}

func TestCurrentObservationPreparesHelperWithoutChangingRunBinding(t *testing.T) {
    service, ref, remote, store := cleanupFixture(t)
    service.Runs.Remote = confirmedObservation{status: "failed"}
    prepared := 0
    service.Runs.Runner = func(_ context.Context, target config.Target, arch string, batch bool) (string, error) {
        if arch != "" || !batch || target.Destination != ref.Connection.Destination {
            t.Fatal(arch, batch, target)
        }
        prepared++
        return "/verified/current/helper", nil
    }
    result, err := service.Runs.Observe(context.Background(), ref, 0, 0, true)
    if err != nil || result.Status != "failed" || prepared != 1 || remote.cleans != 0 {
        t.Fatal(result, err, prepared)
    }
    saved, err := store.Runs(ref.ProjectID)
    if err != nil || saved[0].Runner != ref.Runner || saved[0].Version != wire.Version {
        t.Fatal(saved, err)
    }
}
