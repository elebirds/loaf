package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
)

type fakeChecker struct {
    Target config.Target
    Dir    string
    Batch  bool
}

func (f *fakeChecker) Check(_ context.Context, t config.Target, dir string, batch bool) (ssh.Report, error) {
    f.Target = t
    f.Dir = dir
    f.Batch = batch
    return ssh.Report{OS: "Linux", Arch: "aarch64", RemoteDir: dir, Writable: true, Issues: []string{}}, nil
}

type testOptions struct {
    client.Dependencies
    Options
}

func fixture(t *testing.T) testOptions {
    t.Helper()
    store := config.New(t.TempDir(), "", "")
    return testOptions{
        Dependencies: client.Dependencies{Config: store, State: local.New(store.StateDir), Checker: &fakeChecker{}, CWD: t.TempDir()},
        Options:      Options{Env: map[string]string{"LANG": "C"}, In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}},
    }
}
func execute(ctx context.Context, args []string, o testOptions) int {
    settings, err := o.Config.Load()
    o.Options.Language, o.InitError = settings.Language, err
    o.Dependencies.Trace = o.Options.Trace
    o.App = client.New(o.Dependencies, settings)
    return Execute(ctx, args, o.Options)
}

func run(t *testing.T, o testOptions, args ...string) (int, string, string) {
    t.Helper()
    out, errout := &bytes.Buffer{}, &bytes.Buffer{}
    o.Out = out
    o.Err = errout
    code := execute(context.Background(), args, o)
    return code, out.String(), errout.String()
}

func TestConnectionWorkflowJSON(t *testing.T) {
    o := fixture(t)
    for _, args := range [][]string{{"add", "demo", "--ssh", "ssh -p 2222 root@example.com", "--json"}, {"use", "demo", "--json"}, {"check", "--json"}} {
        code, out, stderr := run(t, o, args...)
        if code != 0 || stderr != "" {
            t.Fatalf("%v: %d %s %s", args, code, out, stderr)
        }
        var e envelope
        if err := json.Unmarshal([]byte(out), &e); err != nil || e.SchemaVersion != 1 || !e.OK {
            t.Fatalf("%s %v", out, err)
        }
    }
    checker := o.Checker.(*fakeChecker)
    if checker.Target.Destination != "root@example.com" || !checker.Batch || !strings.Contains(checker.Dir, "p_") {
        t.Fatalf("%+v", checker)
    }
    if _, err := os.Stat(filepath.Join(o.CWD, "loaf.toml")); !os.IsNotExist(err) {
        t.Fatal("use modified shared project configuration")
    }
}

func TestLocalizedHelpAndParserErrors(t *testing.T) {
    for _, args := range [][]string{{"--help"}, {"add", "--help"}, {"check", "--help"}, {"sync", "--help"}, {"help", "use"}, {"add"}, {"check", "--bad"}, {"check", "--timeout", "bad"}, {"bogus"}} {
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

func TestLanguagePrecedenceAndJSONStability(t *testing.T) {
    o := fixture(t)
    o.Env["LOAF_LANG"] = "zh-Hans"
    _, out, _ := run(t, o, "--help")
    if !strings.Contains(out, "用法") {
        t.Fatal(out)
    }
    _, out, _ = run(t, o, "--lang", "en", "--help")
    if !strings.Contains(out, "Usage") {
        t.Fatal(out)
    }
    var errorsByLanguage []errorView
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "use", "missing", "--json")
        if code != 2 || stderr != "" {
            t.Fatal(code, out, stderr)
        }
        var e envelope
        if err := json.Unmarshal([]byte(out), &e); err != nil {
            t.Fatal(err)
        }
        errorsByLanguage = append(errorsByLanguage, *e.Error)
    }
    if errorsByLanguage[0].Code != errorsByLanguage[1].Code || errorsByLanguage[0].Message == errorsByLanguage[1].Message {
        t.Fatal(errorsByLanguage)
    }
    for _, args := range [][]string{{"--lang", "zh-Hant", "--json", "version"}, {"--lang=", "--json"}} {
        code, out, _ := run(t, o, args...)
        if code != 2 || !json.Valid([]byte(out)) {
            t.Fatal(code, out)
        }
    }
    code, out, _ := run(t, o, "add", "demo", "--json")
    if code != 2 || !strings.Contains(out, "BAD_CONFIG") {
        t.Fatal(code, out)
    }
}

func TestGlobalLanguageAndFlagAlignment(t *testing.T) {
    o := fixture(t)
    if err := os.MkdirAll(filepath.Dir(o.Config.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(o.Config.ConfigFile, []byte("format_version = 1\nlanguage = 'zh-Hans'\n"), 0600); err != nil {
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

func TestPromptCancellationIsNotAFileFailure(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        locale, err := i18n.New(language, nil)
        if err != nil {
            t.Fatal(err)
        }
        output := presenter{locale: locale}
        for _, cause := range []error{interaction.ErrCanceled, context.Canceled} {
            view := output.error(cause)
            if view.Code != "OPERATION_CANCELED" || view.Message != locale.Text("operation_canceled") {
                t.Fatalf("%s: %+v", language, view)
            }
        }
    }
}
