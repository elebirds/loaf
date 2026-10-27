package cli

import (
    "bytes"
    "encoding/json"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
)

func TestNewCommandsAndNonexecutingMigrationErrors(t *testing.T) {
    o := fixture(t)
    for _, old := range []string{"add", "use", "targets", "remove", "ps"} {
        code, out, stderr := run(t, o, old, "anything", "--json")
        var e envelope
        if json.Unmarshal([]byte(out), &e) != nil || code != 2 || stderr != "" || e.Error == nil || e.Error.Reason != "command_replaced" {
            t.Fatal(old, code, out, stderr)
        }
        if _, err := os.Stat(o.Config.ConfigFile); !os.IsNotExist(err) {
            t.Fatal("old command mutated configuration", old, err)
        }
        if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
            t.Fatal("old command created state", old, err)
        }
    }
    if code, out, stderr := run(t, o, "machine", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"targets":[]`) {
        t.Fatal(code, out, stderr)
    }
    if code, out, _ := run(t, o, "--help"); code != 0 || strings.Contains(out, "  ps ") || strings.Contains(out, "  targets ") || !strings.Contains(out, "status") {
        t.Fatal(code, out)
    }
}

func TestSpecificSafeInputDiagnostics(t *testing.T) {
    tests := []struct {
        args             []string
        reason, argument string
    }{
        {[]string{"run", "--wiat", "SECRET"}, "unknown_option", "--wiat"},
        {[]string{"machine", "add"}, "missing_argument", "name"},
        {[]string{"machine", "add", "lab"}, "missing_argument", "--ssh"},
        {[]string{"run", "python3", "train.py"}, "missing_argument", "--"},
        {[]string{"check", "--timeout", "SECRET"}, "invalid_type", "--timeout"},
        {[]string{"check", "--timeout"}, "missing_argument", "--timeout"},
        {[]string{"run", "--wait", "SECRET", "--", "fixture-command"}, "invalid_type", "--wait"},
        {[]string{"run", "--wait", "6m", "--", "fixture-command"}, "out_of_range", "--wait"},
        {[]string{"status", "ID", "--target", "lab"}, "conflicting_arguments", "run-id"},
        {[]string{"outputs", "--depth", "21"}, "out_of_range", "--depth/--limit"},
        {[]string{"forward", "SECRET"}, "invalid_type", "port"},
        {[]string{"forward", "999999999999999999999999999"}, "out_of_range", "port"},
        {[]string{"--lang", "unsupported", "version"}, "invalid_value", "--lang"},
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, tc := range tests {
            o := fixture(t)
            args := append([]string{"--lang", lang, "--json"}, tc.args...)
            code, out, stderr := run(t, o, args...)
            var e envelope
            if json.Unmarshal([]byte(out), &e) != nil || code != 2 || stderr != "" || e.Error == nil {
                t.Fatal(args, code, out, stderr)
            }
            if e.Error.Reason != tc.reason || e.Error.Argument != tc.argument || len(e.Error.HelpCommand) < 2 || strings.Contains(out, "SECRET") {
                t.Fatal(args, out)
            }
        }
    }
}

func TestBrokenConfigurationStillAllowsHelpAndVersion(t *testing.T) {
    for _, contents := range []string{"format_version=1\nunknown_secret='NEVER_ECHO'\n", "format_version=1\nlanguage='unsupported'\n", "format_version=1\n[run]\nwait='never'\n"} {
        o := fixture(t)
        if err := os.MkdirAll(filepath.Dir(o.Config.ConfigFile), 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(o.Config.ConfigFile, []byte(contents), 0600); err != nil {
            t.Fatal(err)
        }
        for _, args := range [][]string{{"--help", "--json"}, {"help", "run", "--json"}, {"version", "--json"}, {"--version", "--json"}} {
            code, out, stderr := run(t, o, args...)
            if code != 0 || stderr != "" || !json.Valid([]byte(out)) || strings.Contains(out, "NEVER_ECHO") {
                t.Fatal(contents, args, code, out, stderr)
            }
        }
        if code, _, _ := run(t, o, "--lang", "unsupported", "--help", "--json"); code != 2 {
            t.Fatal("invalid explicit language ignored")
        }
    }
    o := fixture(t)
    os.MkdirAll(filepath.Dir(o.Config.ConfigFile), 0700)
    os.WriteFile(o.Config.ConfigFile, []byte("format_version=1\nunknown_secret='NEVER_ECHO'\n"), 0600)
    code, out, stderr := run(t, o, "machine", "--json")
    var e envelope
    if code != 2 || stderr != "" || json.Unmarshal([]byte(out), &e) != nil || e.Error.Location == nil || e.Error.Location.File != o.Config.ConfigFile || e.Error.Location.Line != 2 || e.Error.Location.Field != "unknown_secret" || strings.Contains(out, "NEVER_ECHO") {
        t.Fatal(code, out, stderr)
    }
}

func TestHelpMetadataAndBilingualLayout(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        _, out, stderr := run(t, o, "help", "run", "--lang", lang, "--json")
        var e struct{ Data helpResult }
        if stderr != "" || json.Unmarshal([]byte(out), &e) != nil || e.Data.Command != "loaf run" || len(e.Data.Examples) == 0 {
            t.Fatal(out, stderr)
        }
        found := false
        for _, flag := range e.Data.Flags {
            if flag.Name == "wait" {
                found = flag.Type == "duration" && flag.Default == "0s" && flag.Configurable && flag.ConfigKey == "run.wait"
            }
        }
        if !found {
            t.Fatal(out)
        }
        for _, node := range []string{"", "machine", "run", "logs", "pull", "wait", "cloud acquire"} {
            args := append([]string{"help"}, strings.Fields(node)...)
            args = append(args, "--lang", lang, "--json")
            code, out, stderr := run(t, o, args...)
            if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &e) != nil {
                t.Fatal(code, out, stderr)
            }
            // Each description is one logical line. Terminal width, including
            // long flag labels and CJK text, must not insert newlines into it.
            requireLine := func(label, description string) {
                t.Helper()
                for _, line := range strings.Split(e.Data.Help, "\n") {
                    if strings.Contains(line, label+" ") && strings.Contains(line, description) {
                        return
                    }
                }
                t.Fatalf("description wrapped away from %q: %s", label, e.Data.Help)
            }
            for _, flag := range e.Data.Flags {
                requireLine("--"+flag.Name, flag.Description)
            }
            for _, child := range e.Data.Commands {
                requireLine("  "+child.Name, child.Description)
            }
            if node == "pull" && !strings.Contains(e.Data.Help, "[") {
                t.Fatal("optional IDs absent", e.Data.Help)
            }
            if node == "wait" && !strings.Contains(e.Data.Help, "--tag") {
                t.Fatal("tag selection absent", e.Data.Help)
            }
        }
    }
}

func TestResourceHelpMetadataUsesRealConfigurationFields(t *testing.T) {
    o := fixture(t)
    code, out, stderr := run(t, o, "help", "cloud", "acquire", "--json")
    var e struct{ Data helpResult }
    if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &e) != nil {
        t.Fatal(code, out, stderr)
    }
    flags := map[string]helpValue{}
    for _, flag := range e.Data.Flags {
        flags[flag.Name] = flag
    }
    if !flags["gpu-model"].Configurable || flags["gpu-model"].ConfigKey != "resources.gpu_models" || flags["gpu-model"].ListFormat != "csv" || !flags["gpu-model"].Repeatable {
        t.Fatal(out)
    }
    if flags["gpu-count"].Default != float64(1) || flags["profile"].Configurable || flags["ready-timeout"].Configurable {
        t.Fatal(out)
    }
    _, out, _ = run(t, o, "help", "status", "--json")
    if json.Unmarshal([]byte(out), &e) != nil {
        t.Fatal(out)
    }
    for _, flag := range e.Data.Flags {
        if flag.Name == "target" && (flag.Configurable || flag.ConfigKey != "") {
            t.Fatal("history inherited selected target", out)
        }
    }
}

func TestMachinePromptValidatesLocallyAndKeepsInput(t *testing.T) {
    o := fixture(t)
    o.Interactive = true
    o.In = strings.NewReader("ssh host && do-not-execute\nlab-gpu\n")
    code, out, stderr := run(t, o, "machine", "add", "lab")
    if code != 0 || !strings.Contains(out, "lab-gpu") || !strings.Contains(stderr, "Ctrl-C") {
        t.Fatal(code, out, stderr)
    }
    cfg, err := o.Config.Load()
    if err != nil || cfg.Targets["lab"].Destination != "lab-gpu" {
        t.Fatal(cfg, err)
    }
}

func TestCloudConfirmationDisplaysResolvedIdentity(t *testing.T) {
    o, p := cloudFixture(t)
    o.Interactive = true
    o.In = strings.NewReader("no\n")
    code, out, stderr := run(t, o, "cloud", "stop", "pro-existing")
    if code != 2 || p.creates != 0 || !strings.Contains(stderr, "demo") || !strings.Contains(stderr, "pro-existing") || !strings.Contains(stderr, "pro") {
        t.Fatal(code, out, stderr)
    }
}

func TestRunGuidanceKeepsOriginalIDAndFact(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(lang, nil)
        o, remote := runFixture(t)
        code, out, stderr := run(t, o, "--lang", lang, "run", "--wait", "1s", "--", "fixture-command")
        if code != 0 || strings.Count(out, "中文 log") != 1 || !strings.Contains(out, "loaf logs "+remote.req.RunID) {
            t.Fatal(code, out, stderr)
        }
        var rendered bytes.Buffer
        failure := fault.Invalid("--wait", "out_of_range", "invalid_wait")
        p := presenter{out: &rendered, err: &rendered, locale: l, command: "run"}
        if !reflect.DeepEqual(p.error(failure).HelpCommand, []string{"loaf", "help", "run"}) {
            t.Fatal(p.error(failure))
        }
        if _, err := o.Config.Load(); err != nil {
            t.Fatal(err)
        }
    }
}
