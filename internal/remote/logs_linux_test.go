package remote

import (
    "bytes"
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "unicode/utf8"

    "github.com/elebirds/loaf/internal/wire"
)

func TestBoundedLogTailsAndFinalStream(t *testing.T) {
    root := managedRoot(t)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "status", Root: root, ProjectID: "project", RunID: "logs", Tail: 2}
    dir, err := runPath(req)
    if err != nil {
        t.Fatal(err)
    }
    os.Mkdir(dir, 0700)
    code := 7
    if err := save(dir, &wire.RunRecord{FormatVersion: 1, Request: req, Status: "failed", ExitCode: &code}); err != nil {
        t.Fatal(err)
    }
    writeSource(t, dir, "output.log", "one\n中文\nfinal\n")
    observed, err := Observe(context.Background(), req)
    if err != nil || observed.Log.Text != "中文\nfinal\n" || !observed.Log.Truncated {
        t.Fatal(observed, err)
    }
    var out bytes.Buffer
    if err := Follow(context.Background(), req, &out); err != nil {
        t.Fatal(err)
    }
    decoder := json.NewDecoder(&out)
    var first, last wire.LogEvent
    if decoder.Decode(&first) != nil || decoder.Decode(&last) != nil || first.Log.Text != "中文\nfinal\n" || last.Type != "end" || last.Record.ExitCode == nil || *last.Record.ExitCode != 7 {
        t.Fatal(first, last)
    }
    huge := strings.Repeat("x", logLimit*2)
    writeSource(t, dir, "output.log", huge)
    chunk, err := readLog(dir, 100, nil)
    if err != nil || len(chunk.Text) > logLimit || !chunk.Truncated || chunk.Offset != int64(len(huge)) {
        t.Fatal(len(chunk.Text), chunk.Offset, err)
    }
    zero, err := readLog(dir, 0, nil)
    if err != nil || zero.Text != "" || zero.Offset != int64(len(huge)) {
        t.Fatal(zero, err)
    }
    unicode := strings.Repeat("你好", logLimit/2)
    writeSource(t, dir, "output.log", unicode)
    position := int64(0)
    var joined strings.Builder
    for position < int64(len(unicode)) {
        part, err := readLog(dir, 0, &position)
        if err != nil || !utf8.ValidString(part.Text) || part.Offset <= position {
            t.Fatal(part.Offset, err)
        }
        joined.WriteString(part.Text)
        position = part.Offset
    }
    if joined.String() != unicode {
        t.Fatal("stream lost UTF-8 bytes")
    }
    os.Remove(filepath.Join(dir, "output.log"))
    os.Symlink("/etc/passwd", filepath.Join(dir, "output.log"))
    if _, err := readLog(dir, 100, nil); err == nil {
        t.Fatal("followed log symlink")
    }
}

func TestQueryDoesNotCreateDirectoriesOrRewriteUnknownRecords(t *testing.T) {
    root, err := initProject(filepath.Join(t.TempDir(), "project"), "project")
    if err != nil {
        t.Fatal(err)
    }
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "status", Root: root, ProjectID: "project", RunID: "missing"}
    if _, err := Handle(req); err == nil || err.Error() != "RUN_NOT_FOUND" {
        t.Fatal(err)
    }
    if _, err := os.Stat(filepath.Join(root, "runs")); !os.IsNotExist(err) {
        t.Fatal("query created runs directory")
    }
    req.Action = "launch"
    dir, err := runPath(req)
    if err != nil {
        t.Fatal(err)
    }
    os.Mkdir(dir, 0700)
    original := `{"format_version":99,"status":"running"}`
    writeSource(t, dir, "run.json", original)
    req.Args = []string{"/bin/true"}
    if _, err := Handle(req); err == nil || err.Error() != "BAD_FORMAT" {
        t.Fatal(err)
    }
    data, _ := os.ReadFile(filepath.Join(dir, "run.json"))
    if string(data) != original {
        t.Fatal("rewrote unknown run format")
    }
}
