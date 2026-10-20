package wire

import (
    "io/fs"
    "strings"
    "unicode/utf8"
)

// ValidOutputPath accepts concrete work-relative subpaths, not the work root.
// Trailing slashes may be removed by callers before validation.
func ValidOutputPath(name string) bool {
    return name != "." && fs.ValidPath(name) && utf8.ValidString(name) && !strings.ContainsAny(name, "\x00\r\n")
}
