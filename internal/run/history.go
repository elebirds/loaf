package run

import (
    "context"
    "sync"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// HistoryFilters narrows saved submission attempts before querying their bound
// machines. Status filtering happens after observation and never expands a scan.
type HistoryFilters struct {
    Statuses []string   `json:"statuses,omitempty"`
    Target   string     `json:"target,omitempty"`
    Since    *time.Time `json:"since,omitempty"`
    Until    *time.Time `json:"until,omitempty"`
}

// Validate rejects unknown statuses and inverted, inclusive submission boundaries.
func (f HistoryFilters) Validate() error {
    for _, status := range f.Statuses {
        switch status {
        case "starting", "running", "succeeded", "failed", "stopped", "unknown":
        default:
            return fault.New("BAD_CONFIG", "ps_invalid_status", nil)
        }
    }
    if f.Since != nil && f.Until != nil && f.Since.After(*f.Until) {
        return fault.New("BAD_CONFIG", "ps_invalid_range", nil)
    }
    return nil
}

// History is one bounded current-project observation. Total is the complete local
// locator count, Eligible applies local filters, and Examined counts remote/local
// observations. Rows with QueryError are retained even when their status match
// cannot be established; Matched excludes those Unresolved rows.
type History struct {
    Runs       []View         `json:"runs"`
    Total      int            `json:"total"`
    Truncated  bool           `json:"truncated"`
    Eligible   int            `json:"eligible"`
    Examined   int            `json:"examined"`
    Limit      int            `json:"limit"`
    Matched    int            `json:"matched"`
    Unresolved int            `json:"unresolved"`
    Filters    HistoryFilters `json:"filters"`
}

// History queries at most limit locally eligible entries and shares the existing
// 20-second list budget. An unreachable machine never becomes a training failure.
func (s Service) History(ctx context.Context, projectID string, filters HistoryFilters, limit int, batch bool) (History, error) {
    result := History{Runs: []View{}, Limit: limit, Filters: filters}
    if err := filters.Validate(); err != nil {
        return result, err
    }
    if limit < 1 || limit > 1000 {
        return result, fault.New("BAD_CONFIG", "ps_invalid_limit", nil)
    }
    refs, err := s.Index.Runs(projectID)
    if err != nil {
        return result, err
    }
    result.Total = len(refs)
    eligible := make([]config.RunRef, 0, len(refs))
    for _, ref := range refs {
        if filters.Target != "" && ref.Target != filters.Target {
            continue
        }
        if filters.Since != nil && ref.AttemptedAt.Before(*filters.Since) || filters.Until != nil && ref.AttemptedAt.After(*filters.Until) {
            continue
        }
        eligible = append(eligible, ref)
    }
    result.Eligible = len(eligible)
    queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    candidates := eligible[:min(len(eligible), limit)]
    observations := s.observeHistory(queryCtx, candidates, batch)
    failed := false
    for _, item := range observations {
        if !item.examined {
            failed = true
            continue
        }
        observed, err := item.view, item.err
        result.Examined++
        if err != nil {
            failed = true
            if observed.QueryError == "" {
                observed.QueryError = errorCode(err)
            }
            result.Unresolved++
            result.Runs = append(result.Runs, observed)
            continue
        }
        if !filters.matchesStatus(observed.Status) {
            continue
        }
        result.Matched++
        result.Runs = append(result.Runs, observed)
    }
    result.Truncated = result.Examined < result.Eligible
    if failed {
        return result, fault.New("QUERY_PARTIAL", "run_query_partial", nil)
    }
    return result, nil
}

func (f HistoryFilters) matchesStatus(status string) bool {
    if len(f.Statuses) == 0 {
        return true
    }
    for _, allowed := range f.Statuses {
        if status == allowed {
            return true
        }
    }
    return false
}

type historyObservation struct {
    view     View
    err      error
    examined bool
}

func (s Service) observeHistory(ctx context.Context, refs []config.RunRef, batch bool) []historyObservation {
    observations := make([]historyObservation, len(refs))
    jobs := make(chan int)
    var workers sync.WaitGroup
    count := 4
    if !batch {
        // Native SSH authentication may read the terminal. Keep prompts serial
        // rather than interleaving password or host-trust input across targets.
        count = 1
    }
    for i := 0; i < min(count, len(refs)); i++ {
        workers.Add(1)
        go func() {
            defer workers.Done()
            for i := range jobs {
                if ctx.Err() != nil {
                    return
                }
                observed, err := s.Observe(ctx, refs[i], 0, 0, batch)
                observed.Log = nil
                observations[i] = historyObservation{view: observed, err: err, examined: true}
            }
        }()
    }
    // Give each saved alias an early observation before additional entries from
    // the same alias. A slow target must not monopolize all four workers.
    order, remaining := []int{}, []int{}
    seen := map[string]bool{}
    for i, ref := range refs {
        if seen[ref.Target] {
            remaining = append(remaining, i)
        } else {
            seen[ref.Target] = true
            order = append(order, i)
        }
    }
    order = append(order, remaining...)
submit:
    for _, i := range order {
        select {
        case jobs <- i:
        case <-ctx.Done():
            break submit
        }
    }
    close(jobs)
    workers.Wait()
    return observations
}
