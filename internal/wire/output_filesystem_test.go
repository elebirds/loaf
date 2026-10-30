package wire

import "testing"

func TestWindowsOutputNames(t *testing.T) {
    for _, name := range []string{"out/CON.txt", "aux", "out/com1", "LPT².log", "out/a:", "out/a\\b", "out/a.", "out/a ", "out/a?", "out/a\x01", "../escape"} {
        if WindowsOutputPath(name) {
            t.Errorf("accepted %q", name)
        }
    }
    for _, name := range []string{"输出/中文 空格.txt", "out/com10", "out/console", "out/file.name", "out/#literal;"} {
        if !WindowsOutputPath(name) {
            t.Errorf("rejected %q", name)
        }
    }
}
