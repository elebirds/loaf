package fault

import (
    "errors"
    "testing"
)

func TestStagePreservesCauseAndMostSpecificFailure(t *testing.T) {
    original := New("CLOUD_FAILED", "cloud_failed", map[string]any{"ProviderCode": "TRANSPORT"})
    original.Hint = "cloud_ssh_hint"
    staged := At(At(original, "cloud_connection"), "runner_verify")
    var actual *Error
    if !errors.Is(staged, original) || !errors.As(staged, &actual) || actual != original || Stage(staged) != "cloud_connection" {
        t.Fatal(staged, actual, Stage(staged))
    }
    if At(nil, "observe") != nil || Stage(errors.New("plain")) != "" {
        t.Fatal("invented an error or stage")
    }
}
