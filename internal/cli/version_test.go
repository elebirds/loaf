package cli

import (
    "runtime/debug"
    "testing"
)

func TestVersionBuildProvenanceRetainsUnknownState(t *testing.T) {
    data := versionData(nil)
    if data.Revision != "unknown" || data.Modified != nil {
        t.Fatal(data)
    }
    data = versionData(&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "source-commit"}, {Key: "vcs.modified", Value: "true"}}})
    if data.Revision != "source-commit" || !*data.Modified || data.GoVersion == "" {
        t.Fatal(data)
    }
}
