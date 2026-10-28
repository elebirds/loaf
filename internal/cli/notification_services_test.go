package cli

import (
    "encoding/xml"
    "os"
    "strings"
    "testing"
)

func TestOptionalBridgeServiceTemplatesContainOnlyExplicitUserConfiguration(t *testing.T) {
    plist, err := os.ReadFile("../../examples/notify-services/com.elebirds.loaf.notify.plist")
    if err != nil {
        t.Fatal(err)
    }
    var document struct {
        XMLName xml.Name `xml:"plist"`
        Version string   `xml:"version,attr"`
    }
    if err = xml.Unmarshal(plist, &document); err != nil || document.XMLName.Local != "plist" || document.Version != "1.0" {
        t.Fatal(document, err)
    }
    unit, err := os.ReadFile("../../examples/notify-services/loaf-notify.service")
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(string(unit), "ExecStart=/absolute/path/to/loaf notify serve --json") || !strings.Contains(string(unit), "WantedBy=default.target") {
        t.Fatal(string(unit))
    }
    for _, template := range []string{string(plist), string(unit)} {
        if strings.Contains(template, "Bearer") || strings.Contains(template, "CODEX_THREAD_ID") || strings.Contains(template, "User=root") {
            t.Fatal("service stores secrets, guesses a session or escalates privileges")
        }
    }
}
