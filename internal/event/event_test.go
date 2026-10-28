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
