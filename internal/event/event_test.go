package event

import (
    "encoding/json"
    "strings"
    "testing"
    "time"
)

func TestResourceEventDoesNotRequireJob(t *testing.T) {
    e := Envelope{EventVersion: 1, ID: "evt_1", SourceID: "source_1", Sequence: 1, Type: "resource.ready", Subject: Subject{"resource", "instance_1"}, OccurredAt: time.Now(), DataVersion: 1, Data: Data{Status: "ready"}}
    if err := e.Validate(); err != nil {
        t.Fatal(err)
    }
    b, err := json.Marshal(e)
    if err != nil {
        t.Fatal(err)
    }
    for _, private := range []string{"credential", "env", "command", "endpoint"} {
        if strings.Contains(string(b), private) {
            t.Fatalf("public event contains %s", private)
        }
    }
}

func TestCursorBindsSourceIncarnation(t *testing.T) {
    encoded, err := EncodeCursor(Cursor{Version: 1, SourceID: "source_1", Sequence: 0})
    if err != nil {
        t.Fatal(err)
    }
    if _, err = DecodeCursor(encoded, "source_1"); err != nil {
        t.Fatal(err)
    }
    if _, err = DecodeCursor(encoded, "source_2"); err == nil {
        t.Fatal("cursor crossed database incarnation")
    }
    for _, v := range []string{"invalid", strings.Repeat("a", 2049)} {
        if _, err = DecodeCursor(v, "source_1"); err == nil {
            t.Fatal("invalid cursor accepted")
        }
    }
}

func TestSummaryKeepsOriginalProjectAndRelatedOperationIdentities(t *testing.T) {
    exit := 0
    e := Envelope{ID: "evt_1", SourceID: "source_1", Type: "run.succeeded", Subject: Subject{"run", "r_1"}, Correlation: Correlation{ProjectID: "p_1", RunID: "r_1", QueueRequestID: "q_1", CloudRequestID: "c_1"}, Data: Data{ExitCode: &exit, Code: "test_code"}}
    summary := Summary(e)
    for _, want := range []string{"Event evt_1", "run.succeeded", "Source ID: source_1", "Project ID: p_1", "Run ID: r_1", "Queue request ID: q_1", "Resource request ID: c_1", "Exit code: 0", "Code: test_code", "original local project", "already authorized"} {
        if !strings.Contains(summary, want) {
            t.Fatalf("missing %q in %s", want, summary)
        }
    }
    resource := Summary(Envelope{ID: "evt_2", Type: "resource.ready", Subject: Subject{"resource", "instance_1"}, Correlation: Correlation{CloudRequestID: "c_2"}})
    if !strings.Contains(resource, "Resource request ID: c_2") || strings.Contains(resource, "saved Run") || strings.Contains(resource, "Exit code:") {
        t.Fatal(resource)
    }
}
