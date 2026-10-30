package cli

import (
    "context"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
    "strings"
    "testing"
)

type uxBCheckFunc func(context.Context, config.Target, string, bool) (ssh.Report, error)

func (f uxBCheckFunc) Check(ctx context.Context, t config.Target, d string, b bool) (ssh.Report, error) {
    return f(ctx, t, d, b)
}
func TestUXBDirectCheckSummaryKeepsTechnicalDataInJSON(t *testing.T) {
    o, _ := operationFixture(t)
    privatePath := "/fixture/" + strings.Repeat("very-long-path/", 10) + "python"
    o.Checker = uxBCheckFunc(func(_ context.Context, _ config.Target, _ string, _ bool) (ssh.Report, error) {
        return ssh.Report{OS: "Linux", Arch: "aarch64", RemoteDir: "/fixture/project", Remote: map[string]ssh.Tool{"python": {Path: privatePath, Version: "Python fixture-version"}}}, nil
    })
    for _, language := range []string{"en", "zh-Hans"} {
        code, out, diagnostic := run(t, o, "check", "--target", "demo", "--lang", language)
        l, _ := i18n.New(language, nil)
        if code != 0 || diagnostic != "" || strings.Contains(out, privatePath) || strings.Contains(out, "fixture-version") || !strings.Contains(out, l.Text("check_step_authentication")) || !strings.Contains(out, l.Text("probe_unknown")) {
            t.Fatal(code, out, diagnostic)
        }
        o.In = uxCoreNoInput{t}
        code, out, diagnostic = run(t, o, "check", "--target", "demo", "--json", "--lang", language)
        if code != 0 || diagnostic != "" || !strings.Contains(out, privatePath) || !strings.Contains(out, "fixture-version") || strings.Contains(out, "\x1b") {
            t.Fatal(code, out, diagnostic)
        }
    }
}
