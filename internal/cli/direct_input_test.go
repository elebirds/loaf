package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "os"
    "reflect"
    "strings"
    "testing"
)

func TestBareInteractiveEntriesRemainOfflineHelp(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, terminal := range []bool{false, true} {
            for _, args := range [][]string{nil, {"-i"}, {"cloud"}, {"cloud", "-i"}, {"--interactive", "cloud", "--account", "missing"}} {
                out, diagnostic := &bytes.Buffer{}, &bytes.Buffer{}
                code := Execute(context.Background(), append([]string{"--lang", language}, args...), Options{
                    In: uxCoreNoInput{t}, Out: out, Err: diagnostic, Interactive: terminal,
                    InitError: errors.New("configuration unavailable"), Env: map[string]string{"LANG": "C"},
                })
                if code != 0 || out.Len() == 0 || diagnostic.Len() != 0 {
                    t.Fatal(language, terminal, args, code, out, diagnostic)
                }
            }
        }
    }
}

func TestBareHelpBypassesInvalidPersonalLanguageButKeepsExplicitLanguageErrors(t *testing.T) {
    for _, args := range [][]string{nil, {"-i"}, {"cloud"}, {"cloud", "-i"}} {
        out, diagnostic := &bytes.Buffer{}, &bytes.Buffer{}
        options := Options{Language: "invalid-personal-language", In: uxCoreNoInput{t}, Out: out, Err: diagnostic, Env: map[string]string{"LANG": "C"}}
        if code := Execute(context.Background(), args, options); code != 0 || out.Len() == 0 || diagnostic.Len() != 0 {
            t.Fatal(args, code, out, diagnostic)
        }
        out.Reset()
        diagnostic.Reset()
        explicit := append([]string{"--lang", "invalid-explicit-language"}, args...)
        if code := Execute(context.Background(), explicit, options); code != 2 || out.Len() != 0 || diagnostic.Len() == 0 {
            t.Fatal(explicit, code, out, diagnostic)
        }
    }
}

func TestInteractiveFlagDoesNotReplaceFiniteCommands(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, terminal := range []bool{false, true} {
            o, _ := runFixture(t)
            if code, out, diagnostic := run(t, o, "run", "--json", "--", "true"); code != 0 {
                t.Fatal(code, out, diagnostic)
            }
            o.In, o.Interactive = uxCoreNoInput{t}, terminal
            o.Queue, o.Notify, o.Events = &fakeQueue{}, &fakeNotify{}, &fakeEvents{}
            for _, args := range [][]string{{"machine"}, {"machine", "list"}, {"status"}, {"queue"}, {"notify"}, {"events"}} {
                ordinary := append([]string{"--lang", language}, args...)
                code, out, diagnostic := run(t, o, ordinary...)
                interactive := append([]string{"-i"}, ordinary...)
                gotCode, gotOut, gotDiagnostic := run(t, o, interactive...)
                if code != 0 || gotCode != code || gotOut != out || gotDiagnostic != diagnostic {
                    t.Fatal(args, code, gotCode, out, gotOut, diagnostic, gotDiagnostic)
                }
            }
        }
    }
}

func TestInteractiveMissingMachineNamesDoNotOpenManagementMenus(t *testing.T) {
    o := fixture(t)
    o.In, o.Interactive = uxCoreNoInput{t}, true
    for _, command := range []string{"use", "show", "remove"} {
        code, out, diagnostic := run(t, o, "-i", "machine", command)
        if code != 2 || out != "" || diagnostic == "" {
            t.Fatal(command, code, out, diagnostic)
        }
        code, out, diagnostic = run(t, o, "help", "machine", command, "--json")
        var help struct{ Data helpResult }
        if code != 0 || diagnostic != "" || json.Unmarshal([]byte(out), &help) != nil || len(help.Data.Arguments) != 1 || !help.Data.Arguments[0].Required {
            t.Fatal("machine name is not declared required", command, code, out, diagnostic)
        }
    }
    for _, path := range []string{o.Config.ConfigFile, o.State.Path} {
        if _, err := os.Stat(path); !os.IsNotExist(err) {
            t.Fatal("missing command input wrote state", path, err)
        }
    }
}

func TestMachineAddInteractiveInputsShareTheDirectWorkflow(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        o.Interactive = true
        o.In = strings.NewReader("lab\nssh -p 2222 user@host\n")
        code, out, diagnostic := run(t, o, "--lang", language, "-i", "machine", "add")
        if code != 0 || !strings.Contains(out, "lab") || strings.Contains(diagnostic, "[y/N]") {
            t.Fatal(code, out, diagnostic)
        }
        settings, err := o.Config.Load()
        if err != nil || settings.Targets["lab"].Destination != "user@host" {
            t.Fatal(settings, err)
        }
        direct := fixture(t)
        if code, out, diagnostic := run(t, direct, "machine", "add", "lab", "--ssh", "ssh -p 2222 user@host", "--json"); code != 0 {
            t.Fatal(code, out, diagnostic)
        }
        expected, err := direct.Config.Load()
        if err != nil || !reflect.DeepEqual(settings.Targets["lab"], expected.Targets["lab"]) {
            t.Fatal("interactive completion changed the saved connection", settings, expected, err)
        }
        before := settings.Targets["lab"]
        o.In = strings.NewReader("lab\nother-host\n")
        code, _, _ = run(t, o, "-i", "machine", "add")
        after, err := o.Config.Load()
        if code != 2 || err != nil || !reflect.DeepEqual(after.Targets["lab"], before) {
            t.Fatal("input completion implicitly replaced an alias", code, after, err)
        }
        o.In = uxCoreNoInput{t}
        code, _, diagnostic = run(t, o, "-i", "machine", "add", "lab", "--ssh", "replacement", "--replace")
        after, err = o.Config.Load()
        if code != 0 || err != nil || after.Targets["lab"].Destination != "replacement" {
            t.Fatal(code, after, diagnostic, err)
        }
    }
}
