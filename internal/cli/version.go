package cli

import (
    "runtime"
    "runtime/debug"
    "strconv"

    "github.com/elebirds/loaf/internal/wire"
)

func versionData(info *debug.BuildInfo) map[string]any {
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
    return map[string]any{"version": wire.Version, "format_version": wire.FormatVersion, "os": runtime.GOOS, "arch": runtime.GOARCH, "go_version": runtime.Version(), "revision": revision, "modified": modified}
}
