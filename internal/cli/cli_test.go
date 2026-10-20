package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
)

func fixture(t *testing.T) Options {
    t.Helper()
    return Options{Store: config.New(t.TempDir(), "", ""), CWD: t.TempDir(), Env: map[string]string{"LANG": "C"}, In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
}
func run(t *testing.T, o Options, args ...string) (int, string, string) {
    t.Helper()
    out, errout := &bytes.Buffer{}, &bytes.Buffer{}
    o.Out = out
    o.Err = errout
    code := Execute(context.Background(), args, o)
    return code, out.String(), errout.String()
}

func TestLocalizedHelpAndParserErrors(t *testing.T) {
    for _, args := range [][]string{{"--help"}, {"version", "--help"}, {"help", "version"}, {"version", "extra"}, {"version", "--bad"}, {"bogus"}} {
        for _, lang := range []string{"en", "zh-Hans"} {
            o := fixture(t)
            code, out, stderr := run(t, o, append([]string{"--lang", lang}, args...)...)
            if out+stderr == "" {
                t.Fatalf("empty response: %v", args)
            }
            if strings.Contains(strings.Join(args, " "), "help") {
                if code != 0 {
                    t.Fatalf("help failed %v: %s", args, stderr)
                }
            } else if code != 2 {
                t.Fatalf("invalid args %v: %d %s", args, code, stderr)
            }
            if lang == "zh-Hans" && (strings.Contains(out+stderr, "Usage:") || strings.Contains(out+stderr, "unknown flag") || strings.Contains(out+stderr, "unknown command")) {
                t.Fatalf("untranslated parser/help: %s %s", out, stderr)
            }
        }
    }
}

func TestGlobalLanguageAndFlagAlignment(t *testing.T) {
    o := fixture(t)
    if err := os.MkdirAll(filepath.Dir(o.Store.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(o.Store.ConfigFile, []byte("format_version = 1\nlanguage = 'zh-Hans'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    _, out, _ := run(t, o, "--help")
    if !strings.Contains(out, "用法") {
        t.Fatal(out)
    }
    o.Env["LOAF_LANG"] = "en"
    _, out, _ = run(t, o, "--help")
    if !strings.Contains(out, "Usage") {
        t.Fatal(out)
    }
    if padFlag("--lang <值>", 22) != "--lang <值>"+strings.Repeat(" ", 11) {
        t.Fatalf("CJK flag width: %q", padFlag("--lang <值>", 22))
    }
}

func TestVersionAndLanguageErrorsAreStructured(t *testing.T) {
    o := fixture(t)
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "version", "--json")
        var e envelope
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &e) != nil || !e.OK || e.SchemaVersion != 1 {
            t.Fatal(code, out, stderr)
        }
    }
    for _, args := range [][]string{{"--lang", "zh-Hant", "--json", "version"}, {"--lang=", "--json"}} {
        code, out, stderr := run(t, o, args...)
        if code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatal(code, out, stderr)
        }
    }
}
