package interaction

import (
    "testing"
    "unicode/utf16"

    xwindows "github.com/charmbracelet/x/windows"
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

func TestRichConsoleInputDoesNotTypeAltNumpadCompositionDigits(t *testing.T) {
    reader := &richConsoleInput{}
    for _, key := range []uint16{0x66, 0x63, 0x66, 0x63} {
        reader.appendKey(xwindows.KeyEventRecord{KeyDown: true, VirtualKeyCode: key, ControlKeyState: xwindows.LEFT_ALT_PRESSED, RepeatCount: 1})
    }
    for _, unit := range utf16.Encode([]rune("🔑")) {
        reader.appendKey(xwindows.KeyEventRecord{VirtualKeyCode: 0x12, Char: rune(unit), RepeatCount: 1})
    }
    if string(reader.pending) != "🔑" {
        t.Fatalf("typed Unicode composition as digits: %q", reader.pending)
    }
}
