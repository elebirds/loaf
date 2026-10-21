package run

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

func versionMismatch(actual string) *fault.Error {
    f := fault.New("VERSION_MISMATCH", "runner_version_mismatch", map[string]any{"Expected": wire.Version, "Actual": actual})
    f.Hint = "runner_version_hint"
    return f
}

func currentVersion(ref config.RunRef) error {
    if ref.Version != wire.Version {
        return versionMismatch(ref.Version)
    }
    return nil
}

// prepareRef verifies the current control helper, never changes the saved run
// version, and never replaces or restarts a live supervisor.
func (s Service) prepareRef(ctx context.Context, ref config.RunRef, batch bool) (config.RunRef, error) {
    if err := currentVersion(ref); err != nil {
        return ref, err
    }
    if s.Runner == nil {
        return ref, nil
    }
    setup, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()
    runner, err := s.Runner(setup, ref.Connection, "", batch)
    if err != nil {
        return ref, err
    }
    ref.Runner = runner
    return ref, nil
}
