package main

import (
    "bytes"
    "testing"
)

func TestFourSpacesAndLiterals(t *testing.T) {
    source := []byte("package p\nfunc f() {\n\tvalue := `raw\n\tkeep this tab\n`\n\t_ = value\n}\n")
    result, err := spaceFormat("test.go", source)
    if err != nil {
        t.Fatal(err)
    }
    if !bytes.Contains(result, []byte("\n    value :=")) || !bytes.Contains(result, []byte("\n\tkeep this tab\n")) {
        t.Fatalf("indent/literal changed: %q", result)
    }
    again, err := spaceFormat("test.go", result)
    if err != nil {
        t.Fatal(err)
    }
    if !bytes.Equal(result, again) {
        t.Fatal("formatter is not idempotent")
    }
}
