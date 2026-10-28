package queue

import "testing"

func TestPolicyCannotBypassAdmission(t *testing.T) {
    candidates := []Candidate{{ID: "q_1", Sequence: 1}, {ID: "q_2", Sequence: 2}}
    if err := ValidateSelection(candidates, 1, []string{"q_1"}); err != nil {
        t.Fatal(err)
    }
    for _, ids := range [][]string{{"foreign"}, {"q_1", "q_1"}, {"q_1", "q_2"}} {
        if ValidateSelection(candidates, 1, ids) == nil {
            t.Fatalf("unsafe decision accepted: %v", ids)
        }
    }
    if ValidateSelection(candidates, 0, []string{"q_1"}) == nil {
        t.Fatal("policy bypassed full capacity")
    }
}
