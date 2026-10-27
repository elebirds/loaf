package local

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
)

func TestCheckpointMonotonicPrivateAndRejectsUnknownOrSymlink(t *testing.T) {
    p := filepath.Join(t.TempDir(), "cursor.json")
    token := func(n int64) string {
        s, _ := wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: "v", Root: "/r", ProjectID: "p", RunID: "r", FileID: "1:2", Offset: n, Boundary: strings.Repeat("a", 64)})
        return s
    }
    if err := SaveLogCheckpoint(context.Background(), p, token(5)); err != nil {
        t.Fatal(err)
    }
    if err := SaveLogCheckpoint(context.Background(), p, token(4)); err == nil {
        t.Fatal("rewound")
    }
    if got, err := ReadLogCheckpoint(p); err != nil || got != token(5) {
        t.Fatal(got, err)
    }
    info, _ := os.Stat(p)
    if info.Mode().Perm() != 0600 {
        t.Fatal(info.Mode())
    }
    link := filepath.Join(t.TempDir(), "link")
    _ = os.Symlink(p, link)
    if err := SaveLogCheckpoint(context.Background(), link, token(6)); err == nil {
        t.Fatal("followed link")
    }
    _ = os.WriteFile(p, []byte(`{"format_version":99}`), 0600)
    if err := SaveLogCheckpoint(context.Background(), p, token(6)); err == nil {
        t.Fatal("overwrote unknown")
    }
}
