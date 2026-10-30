package interaction

import (
    "testing"
    "unicode/utf16"
)

func TestRichConsoleInputOwnsCompleteUnicodeChunks(t *testing.T) {
    reader := &richConsoleInput{}
    units := utf16.Encode([]rune("中文 🔑"))
    for _, unit := range units {
        reader.appendUnit(rune(unit))
    }
    if string(reader.pending) != "中文 🔑" {
        t.Fatalf("changed Unicode: %q", reader.pending)
    }
    // Bytes handed to the event parser are copied, not a borrowed record buffer.
    chunk := append([]byte(nil), reader.pending...)
    reader.pending = reader.pending[:0]
    reader.appendUnit('x')
    if string(chunk) != "中文 🔑" {
        t.Fatalf("rewrote parser-owned chunk: %q", chunk)
    }
    if _, named := any(reader).(interface{ Name() string }); named {
        t.Fatal("input exposes the racing native serializer contract")
    }
}
