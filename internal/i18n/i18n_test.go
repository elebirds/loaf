package i18n

import (
    "io/fs"
    "reflect"
    "regexp"
    "sort"
    "strings"
    "testing"

    "github.com/pelletier/go-toml/v2"
)

func TestLanguageSelection(t *testing.T) {
    for _, tc := range []struct {
        preferred, system, want string
        bad                     bool
    }{
        {"en", "zh_CN.UTF-8", "en", false}, {"auto", "zh_CN.UTF-8", "zh-Hans", false}, {"auto", "en_US.UTF-8", "en", false},
        {"zh-CN", "", "zh-Hans", false}, {"zh-Hans-SG", "", "zh-Hans", false}, {"auto", "zh_TW.UTF-8", "en", false},
        {"zh-Hant", "", "en", true}, {"auto", "C.UTF-8", "en", false}, {"C", "", "en", false}, {"fr", "", "en", true},
    } {
        l, err := New(tc.preferred, map[string]string{"LANG": tc.system})
        if (err != nil) != tc.bad || l.Locale != tc.want {
            t.Errorf("%+v: %s %v", tc, l.Locale, err)
        }
    }
    l, err := New("auto", map[string]string{"LC_ALL": "en_US.UTF-8", "LC_MESSAGES": "zh_CN.UTF-8", "LANG": "zh_CN.UTF-8"})
    if err != nil || l.Locale != "en" {
        t.Fatal(l, err)
    }
}

func TestCatalogCompletenessAndRendering(t *testing.T) {
    files, err := fs.Glob(catalogs, "locales/active.*.toml")
    if err != nil {
        t.Fatal(err)
    }
    messages := make([]map[string]map[string]string, len(files))
    parameters := regexp.MustCompile(`\{\{\.([A-Za-z]+)\}\}`)
    for i, file := range files {
        data, err := catalogs.ReadFile(file)
        if err != nil {
            t.Fatal(err)
        }
        if err := toml.Unmarshal(data, &messages[i]); err != nil {
            t.Fatal(err)
        }
    }
    for i := 1; i < len(messages); i++ {
        if len(messages[0]) != len(messages[i]) {
            t.Fatalf("catalog key count mismatch: %s", files[i])
        }
    }
    for id, forms := range messages[0] {
        for i := 1; i < len(messages); i++ {
            other, exists := messages[i][id]
            if !exists {
                t.Fatalf("missing key %s in %s", id, files[i])
            }
            a := parameters.FindAllString(forms["other"], -1)
            b := parameters.FindAllString(other["other"], -1)
            sort.Strings(a)
            sort.Strings(b)
            if !reflect.DeepEqual(a, b) {
                t.Fatalf("parameters differ: %s", id)
            }
        }
        for _, file := range files {
            locale := strings.TrimSuffix(strings.TrimPrefix(file, "locales/active."), ".toml")
            l, err := New(locale, nil)
            if err != nil {
                t.Fatal(err)
            }
            rendered := l.Text(id, map[string]any{"Name": "example", "Query": "example", "Notify": "notify", "Wait": "0s", "Completed": 2, "Eligible": 4, "Examined": 3, "Interrupted": 0, "Limit": 20, "Pending": 1, "Selected": 3, "State": "running", "Unexamined": 1, "Unresolved": 1, "Depth": 2, "Failed": 1, "Max": 1000, "Min": 0, "Source": "default", "End": 10, "Queued": 2, "Start": 1, "GPU": "24", "CPU": "16", "Memory": "64", "Disk": "50", "Driver": "560.35", "CUDA": "12.6", "Request": "c_123", "Argument": "--wait", "Line": 1, "Column": 2, "Field": "run.wait", "ID": "r_123", "Region": "west-D", "Value": "20s", "Command": "loaf add", "Languages": "en, zh-Hans", "Target": "demo", "Destination": "host", "Root": "/tmp/中文", "Version": "dev", "Millis": 1, "Masters": 1, "Reconnects": 1, "Retries": 1, "Resolutions": 2, "Outcome": "ok", "Code": "SSH_FAILED", "Reason": "private master exited", "Client": "dev", "Remote": "/remote", "Paths": []string{"outputs"}, "Expected": "dev", "Actual": "different", "Path": "/tmp/runner", "Count": 3, "Total": 5, "Matched": 2, "Bytes": 128, "Offset": 4, "Mode": "mirror", "Provider": "autodl", "Product": "standard", "Account": "lab", "Username": "student", "Price": "1.500", "Currency": "CNY", "Stage": "ticket exchange", "ProviderCode": "INVALID_RESPONSE", "HTTPStatus": 200})
            if rendered == "" || strings.Contains(rendered, "<no value>") {
                t.Fatal(id)
            }
        }
    }
}
