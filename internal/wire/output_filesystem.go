package wire

import (
    "crypto/sha256"
    "encoding/hex"
    "sort"
    "strings"
    "unicode/utf16"
)

// OutputFilesystem describes one download's destination, not the remote run.
type OutputFilesystem struct {
    Name      string `json:"name"`
    Symlinks  bool   `json:"symlinks"`
    Namespace string `json:"namespace,omitempty"`
}

// OutputName is a transient name/type used to reject Windows collisions before rsync.
type OutputName struct {
    Path string `json:"path"`
    Kind string `json:"kind"`
}

// WindowsOutputPath accepts names that map literally to one Win32 NTFS path.
// Reserved devices include their extensions and the documented superscript digits.
func WindowsOutputPath(name string) bool {
    if !ValidOutputPath(name) {
        return false
    }
    for _, part := range strings.Split(name, "/") {
        if strings.ContainsAny(part, `<>:"\|?*`) || strings.TrimRight(part, " .") != part || len(utf16.Encode([]rune(part))) > 255 {
            return false
        }
        for _, r := range part {
            if r < 32 {
                return false
            }
        }
        base, _, _ := strings.Cut(strings.ToUpper(part), ".")
        switch base {
        case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
            return false
        }
        for _, prefix := range []string{"COM", "LPT"} {
            suffix := strings.TrimPrefix(base, prefix)
            if suffix != base && strings.Contains("123456789¹²³", suffix) && len([]rune(suffix)) == 1 {
                return false
            }
        }
    }
    return true
}

// OutputNamespace binds the validated name/type set while permitting live data
// and size/timestamp changes. A second validation precedes native rsync sending.
func OutputNamespace(names []OutputName) string {
    sorted := append([]OutputName(nil), names...)
    sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
    digest := sha256.New()
    for _, entry := range sorted {
        digest.Write([]byte(entry.Path + "\x00" + entry.Kind + "\x00"))
    }
    return hex.EncodeToString(digest.Sum(nil))
}
