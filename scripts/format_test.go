package main

import (
    "bytes"
    "io"
    "os"
    "path/filepath"
    "strings"
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

func TestFormatSelectedFilesAndDirectories(t *testing.T) {
    root := t.TempDir()
    raw := []byte("package p\nfunc f(){\n\tprintln(1)\n}\n")
    for _, name := range []string{"selected/a.go", "selected/nested/b.go", "selected/docs/c.go", "outside.go"} {
        path := filepath.Join(root, name)
        if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(path, raw, 0600); err != nil {
            t.Fatal(err)
        }
    }
    selected := filepath.Join(root, "selected/a.go")
    if failed, err := formatPaths([]string{selected}, false, io.Discard); err != nil || failed {
        t.Fatal(failed, err)
    }
    assertBytes := func(name string, unchanged bool) {
        t.Helper()
        value, err := os.ReadFile(filepath.Join(root, name))
        if err != nil || bytes.Equal(value, raw) != unchanged {
            t.Fatalf("%s unchanged=%v: %q %v", name, unchanged, value, err)
        }
    }
    assertBytes("selected/a.go", false)
    assertBytes("selected/nested/b.go", true)
    assertBytes("outside.go", true)
    if failed, err := formatPaths([]string{filepath.Join(root, "selected"), selected}, false, io.Discard); err != nil || failed {
        t.Fatal(failed, err)
    }
    assertBytes("selected/nested/b.go", false)
    assertBytes("selected/docs/c.go", true)
    assertBytes("outside.go", true)
}

func TestFormatCheckIsReadOnlyAndReportsEachPathOnce(t *testing.T) {
    root := t.TempDir()
    path := filepath.Join(root, "a.go")
    raw := []byte("package p\nfunc f(){\n\tprintln(1)\n}\n")
    if err := os.WriteFile(path, raw, 0600); err != nil {
        t.Fatal(err)
    }
    var output bytes.Buffer
    failed, err := formatPaths([]string{path, root}, true, &output)
    value, readErr := os.ReadFile(path)
    if err != nil || !failed || output.String() != path+"\n" || readErr != nil || !bytes.Equal(value, raw) {
        t.Fatal(failed, err, output.String(), readErr, string(value))
    }
    if _, err := formatPaths([]string{filepath.Join(root, "missing.go")}, false, io.Discard); err == nil {
        t.Fatal("missing path was silently ignored")
    }
}

func TestFormatWithoutPathsRetainsTreeDefault(t *testing.T) {
    root := t.TempDir()
    t.Chdir(root)
    if err := os.WriteFile("a.go", []byte("package p\nfunc f(){println(1)}\n"), 0600); err != nil {
        t.Fatal(err)
    }
    var output bytes.Buffer
    if failed, err := formatPaths(nil, true, &output); err != nil || !failed || strings.TrimSpace(output.String()) != "a.go" {
        t.Fatal(failed, err, output.String())
    }
    if failed, err := formatPaths(nil, false, io.Discard); err != nil || failed {
        t.Fatal(failed, err)
    }
    if failed, err := formatPaths(nil, true, io.Discard); err != nil || failed {
        t.Fatal(failed, err)
    }
}
