package task

import (
    "strings"
    "testing"
)

func TestRunMetadataRejectsInvalidLabels(t *testing.T) {
    for _, m := range []RunMetadata{{Name: "bad\nname"}, {Name: string([]byte{255})}, {Name: strings.Repeat("界", 129)}, {Tags: []string{""}}, {Tags: []string{strings.Repeat("x", 65)}}, {Tags: make([]string, 17)}} {
        if _, err := NormalizeRunMetadata(m); err == nil {
            t.Fatal(m)
        }
    }
}
