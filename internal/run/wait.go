package run

import (
    "context"
    "slices"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// WaitResult describes the complete frozen selection, including unresolved rows.
type WaitResult struct {
    Runs        []View `json:"runs"`
    Selected    int    `json:"selected"`
    Examined    int    `json:"examined"`
    Unexamined  int    `json:"unexamined"`
    Completed   int    `json:"completed"`
    Pending     int    `json:"pending"`
    Interrupted int    `json:"interrupted"`
    QueryErrors int    `json:"query_errors"`
    TimedOut    bool   `json:"timed_out"`
}

// Freeze selects explicit IDs or all exact tags once, without remote access.
func (s Service) Freeze(projectID string, ids, tags []string) ([]config.RunRef, error) {
    if (len(ids) == 0) == (len(tags) == 0) || len(ids) > 1000 {
        return nil, fault.New("BAD_CONFIG", "wait_selection", nil)
    }
    if _, err := config.NormalizeRunMetadata(config.RunMetadata{Tags: tags}); err != nil {
        return nil, err
    }
    refs, err := s.Index.Runs(projectID)
    if err != nil {
        return nil, err
    }
    selected := []config.RunRef{}
    seen := map[string]bool{}
    if len(ids) > 0 {
        for _, id := range ids {
            if seen[id] {
                return nil, fault.New("BAD_CONFIG", "wait_selection", nil)
            }
            seen[id] = true
            found := false
            for _, ref := range refs {
                if ref.ID == id {
                    selected = append(selected, ref)
                    found = true
                    break
                }
            }
            if !found {
                return nil, fault.New("RUN_NOT_FOUND", "run_not_found", nil)
            }
        }
    } else {
        for _, ref := range refs {
            ref, err = s.describe(ref)
            if err != nil {
                return nil, err
            }
            matches := true
            for _, tag := range tags {
                if !slices.Contains(ref.Tags, tag) {
                    matches = false
                    break
                }
            }
            if matches {
                selected = append(selected, ref)
            }
        }
    }
    if len(selected) == 0 || len(selected) > 1000 {
        return nil, fault.New("BAD_CONFIG", "wait_selection", nil)
    }
    for i, ref := range selected {
        selected[i], err = s.describe(ref)
        if err != nil {
            return nil, err
        }
    }
    return selected, nil
}

// Wait is foreground observation only. Query failures remain pending, and
// cancellation never sends a stop. Every poll shares the bounded history pool.
func (s Service) Wait(ctx context.Context, refs []config.RunRef, timeout time.Duration, batch bool, changed func(View)) (WaitResult, error) {
    result := WaitResult{Runs: []View{}, Selected: len(refs)}
    if len(refs) == 0 || len(refs) > 1000 || timeout <= 0 || timeout > 24*time.Hour {
        return result, fault.New("BAD_CONFIG", "wait_timeout", nil)
    }
    for _, ref := range refs {
        result.Runs = append(result.Runs, view(ref, nil))
    }
    seen := make([]bool, len(refs))
    waiting, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    rotation := 0
    for {
        pending, indices := []config.RunRef{}, []int{}
        for i, v := range result.Runs {
            if !wire.Terminal(v.Status) || v.QueryError != "" {
                pending = append(pending, refs[i])
                indices = append(indices, i)
            }
        }
        if len(pending) > 0 {
            n := rotation % len(pending)
            pending = append(pending[n:], pending[:n]...)
            indices = append(indices[n:], indices[:n]...)
        }
        round, stop := context.WithTimeout(waiting, 20*time.Second)
        observations := s.observeBounded(round, pending, batch, 20*time.Second)
        stop()
        rotation++
        for j, item := range observations {
            if !item.examined {
                continue
            }
            i := indices[j]
            // The enclosing observation budget is not evidence that a target
            // failed. Keep the last completed query, dated and explicitly marked
            // interrupted, rather than replacing it with local cancellation.
            if item.interrupted && seen[i] {
                result.Runs[i].QueryInterrupted = true
                continue
            }
            seen[i] = true
            next := item.view
            next.QueryInterrupted = item.interrupted
            if item.err != nil && next.QueryError == "" {
                next.QueryError = errorCode(item.err)
            }
            old := result.Runs[i]
            if changed != nil && (old.Status != next.Status || old.Phase != next.Phase || old.QueryError != next.QueryError) {
                changed(next)
            }
            result.Runs[i] = next
        }
        result.Completed, result.Examined, result.QueryErrors, result.Interrupted = 0, 0, 0, 0
        for i, v := range result.Runs {
            if v.QueryInterrupted {
                result.Interrupted++
            }
            if seen[i] {
                result.Examined++
            }
            if wire.Terminal(v.Status) && v.QueryError == "" {
                result.Completed++
            }
            if v.QueryError != "" {
                result.QueryErrors++
            }
        }
        result.Pending = result.Selected - result.Completed
        result.Unexamined = result.Selected - result.Examined
        if result.Pending == 0 {
            return result, nil
        }
        if waiting.Err() != nil {
            if ctx.Err() != nil {
                return result, fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", ctx.Err())
            }
            result.TimedOut = true
            return result, fault.New("WAIT_TIMEOUT", "wait_timed_out", nil)
        }
        timer := time.NewTimer(2 * time.Second)
        select {
        case <-waiting.Done():
            timer.Stop()
        case <-timer.C:
        }
    }
}
