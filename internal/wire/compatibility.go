package wire

import "regexp"

var formalV1 = regexp.MustCompile(`^v1\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// StableV1 identifies the first supported release series. Prereleases and
// development builds do not acquire a stable-data compatibility promise.
func StableV1(version string) bool { return formalV1.MatchString(version) }

// CompatibleVersion is used only after the owning boundary has validated its
// supported wire/record/schema format. Software identity is not that format:
// formal 1.x peers share the baseline contract; development peers must match.
func CompatibleVersion(current, origin string) bool {
    return current != "" && current == origin || StableV1(current) && StableV1(origin)
}

// StateSeries prevents a development database from becoming a stable database
// merely by moving it to another directory. The schema remains separately owned.
func StateSeries(version string) string {
    if StableV1(version) {
        return "stable-v1"
    }
    return "development"
}

// StateNamespace is fixed for formal 1.x releases, independently of schema and
// software updates. Development uses a separate, unsupported state generation.
func StateNamespace(version string) string {
    if StableV1(version) {
        return "stable-v1"
    }
    return "state-v4"
}
