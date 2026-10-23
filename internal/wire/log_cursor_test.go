package wire

import (
    "encoding/base64"
    "strings"
    "testing"
)

func TestLogCursorIsBoundedVersionedAndRunBound(t *testing.T) {
    c := LogCursor{FormatVersion: 1, Version: Version, Root: "/tmp/项目", ProjectID: "p", RunID: "r", FileID: "1:2", Offset: 12, Boundary: strings.Repeat("a", 64)}
    token, err := EncodeLogCursor(c)
    if err != nil {
        t.Fatal(err)
    }
    got, err := DecodeLogCursor(token)
    if err != nil || got != c || !got.Matches(Request{Version: Version, Root: c.Root, ProjectID: "p", RunID: "r"}) || got.Matches(Request{Version: Version, Root: c.Root, ProjectID: "p", RunID: "other"}) {
        t.Fatal(got, err)
    }
    for _, bad := range []string{"", "not-json", strings.Repeat("x", 8193), base64.RawURLEncoding.EncodeToString([]byte(`{"format_version":99}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"format_version":1,"unknown":true}`))} {
        if _, err := DecodeLogCursor(bad); err == nil {
            t.Fatal(bad)
        }
    }
}
