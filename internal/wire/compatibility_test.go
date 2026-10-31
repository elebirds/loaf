package wire

import "testing"

func TestFormalV1CompatibilityAndDevelopmentBoundary(t *testing.T) {
    for _, value := range []string{"v1.0.0", "v1.1.0", "v1.99.12", "v1.0.0+build.12"} {
        if !StableV1(value) || StateNamespace(value) != "stable-v1" || StateSeries(value) != "stable-v1" {
            t.Fatal("formal version rejected", value)
        }
        if !CompatibleVersion("v1.1.0", value) || !CompatibleVersion(value, "v1.0.0") {
            t.Fatal("same baseline peers rejected", value)
        }
    }
    for _, value := range []string{"", "dev", "test", "v0.6.0", "v1.0.0-rc.1", "v1.1.0-dev.abc", "v2.0.0", "v1.01.0", "v1.0.00", "1.0.0", "v1.0.0+"} {
        if StableV1(value) || CompatibleVersion("v1.0.0", value) || StateNamespace(value) == "stable-v1" || StateSeries(value) == "stable-v1" {
            t.Fatal("unsupported version crossed stable boundary", value)
        }
    }
    if !CompatibleVersion("dev", "dev") || CompatibleVersion("dev", "other-dev") || CompatibleVersion("", "") {
        t.Fatal("development identity matching changed")
    }
}
