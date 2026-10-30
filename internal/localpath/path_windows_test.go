package localpath

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestWindowsCanonicalDirectoryIdentity(t *testing.T) {
    root := filepath.Join(t.TempDir(), "项目 Mixed Case")
    if err := os.Mkdir(root, 0700); err != nil {
        t.Fatal(err)
    }
    original, err := Canonical(root)
    if err != nil {
        t.Fatal(err)
    }
    for _, spelling := range []string{strings.ToLower(root), strings.ToUpper(root), filepath.ToSlash(root), root + `\.`} {
        got, err := Canonical(spelling)
        if err != nil || got != original {
            t.Fatalf("%q -> %q, want %q: %v", spelling, got, original, err)
        }
    }
    if Contains(`C:\project`, `D:\results`) {
        t.Fatal("cross-volume paths overlap")
    }
    for _, invalid := range []string{`\\server\share`, `\\?\C:\project`, `C:relative`, `C:\file:stream`} {
        if err := Supported(invalid); err == nil {
            t.Fatal("accepted", invalid)
        }
    }
    uri := FileURL(`C:\中文 space\state.db`)
    if got := uri.String(); got != "file:///C:/%E4%B8%AD%E6%96%87%20space/state.db" {
        t.Fatal(got)
    }
}
