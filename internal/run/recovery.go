package run

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/task"
)

// RecoveryOptions opts into a total reconnection waiting budget. Zero keeps the
// original one-retry contract. Progress reports only stable operational states.
type RecoveryOptions struct {
    Budget   time.Duration
    Progress func(string)
}

func (o RecoveryOptions) notify(state string) {
    if o.Progress != nil {
        o.Progress(state)
    }
}

// FollowRecovering extends opt-in recovery without changing existing callers.
func (s Service) FollowRecovering(ctx context.Context, ref task.RunRef, wait time.Duration, tail int, batch bool, cursor string, options RecoveryOptions, emit func(View, string) error) (View, error) {
    if options.Budget < 0 || options.Budget > 24*time.Hour {
        return view(ref, nil), fault.New("BAD_CONFIG", "log_recovery_budget", nil)
    }
    return s.follow(ctx, ref, wait, tail, batch, cursor, true, options, emit)
}
