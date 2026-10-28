package queue

import "slices"

// FIFO selects the oldest confirmed admissions without mutating its input.
// Sequence is assigned by the remote ledger, not a client clock.
type FIFO struct{}

func (FIFO) Select(candidates []Candidate, slots int) ([]string, error) {
    if slots < 0 {
        return nil, ErrSelection
    }
    ordered := slices.Clone(candidates)
    slices.SortFunc(ordered, func(a, b Candidate) int {
        if a.Sequence < b.Sequence {
            return -1
        }
        if a.Sequence > b.Sequence {
            return 1
        }
        if a.ID < b.ID {
            return -1
        }
        if a.ID > b.ID {
            return 1
        }
        return 0
    })
    selected := make([]string, 0, min(slots, len(ordered)))
    for _, c := range ordered[:min(slots, len(ordered))] {
        selected = append(selected, c.ID)
    }
    if err := ValidateSelection(candidates, slots, selected); err != nil {
        return nil, err
    }
    return selected, nil
}
