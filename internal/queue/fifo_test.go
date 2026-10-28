package queue

import (
    "slices"
    "testing"
)

func TestFIFOUsesAdmissionSequenceAndDoesNotModifyCandidates(t *testing.T) {
    input := []Candidate{{"q_3", 3}, {"q_1", 1}, {"q_2", 2}}
    before := slices.Clone(input)
    selected, err := (FIFO{}).Select(input, 2)
    if err != nil || !slices.Equal(selected, []string{"q_1", "q_2"}) {
        t.Fatalf("selection=%v, error=%v", selected, err)
    }
    if !slices.Equal(input, before) {
        t.Fatal("policy mutated admission data")
    }
    selected, err = (FIFO{}).Select(input, 0)
    if err != nil || len(selected) != 0 {
        t.Fatal("full queue dispatched a request")
    }
}

func TestSelectionRejectsDuplicateAndForeignIDsWithinCapacity(t *testing.T) {
    candidates := []Candidate{{"q_1", 1}, {"q_2", 2}}
    for _, selection := range [][]string{{"q_1", "q_1"}, {"foreign"}} {
        if ValidateSelection(candidates, 2, selection) == nil {
            t.Fatalf("accepted %v", selection)
        }
    }
}
