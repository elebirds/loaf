package cli

import (
    "runtime/debug"
    "testing"
)

func TestVersionBuildProvenanceRetainsUnknownState(t *testing.T) {
    data := versionData(nil)
    if data["revision"] != "unknown" || data["modified"].(*bool) != nil {
        t.Fatal(data)
    }
    data = versionData(&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "source-commit"}, {Key: "vcs.modified", Value: "true"}}})
    if data["revision"] != "source-commit" || !*data["modified"].(*bool) || data["go_version"] == "" {
        t.Fatal(data)
    }
}
