package cli

import (
    "runtime"
    "runtime/debug"
    "strconv"

    "github.com/elebirds/loaf/internal/wire"
)

type versionResult struct {
    Version       string `json:"version"`
    FormatVersion int    `json:"format_version"`
    OS            string `json:"os"`
    Arch          string `json:"arch"`
    GoVersion     string `json:"go_version"`
    Revision      string `json:"revision"`
    Modified      *bool  `json:"modified"`
}

func versionData(info *debug.BuildInfo) versionResult {
    revision := "unknown"
    var modified *bool
    if info != nil {
        for _, setting := range info.Settings {
            switch setting.Key {
            case "vcs.revision":
                revision = setting.Value
            case "vcs.modified":
                if value, err := strconv.ParseBool(setting.Value); err == nil {
                    modified = &value
                }
            }
        }
    }
    return versionResult{Version: wire.Version, FormatVersion: wire.FormatVersion, OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), Revision: revision, Modified: modified}
}
