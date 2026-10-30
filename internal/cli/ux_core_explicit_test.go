package cli

import (
    "encoding/json"
    "strings"
    "testing"
)

func TestUXCoreTerminalDoesNotImplicitlyEnableForms(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{nil, {"cloud"}, {"machine"}, {"status"}, {"queue"}, {"notify"}, {"events"}, {"--interactive=false"}, {"cloud", "--interactive=false"}} {
            o := fixture(t)
            if len(args) > 0 && args[0] == "status" {
                o, _, _ = psFixture(t)
            }
            o.Interactive, o.TerminalOutput, o.TerminalError = true, true, true
            o.In = uxCoreNoInput{t}
            o.Queue, o.Notify, o.Events = &fakeQueue{}, &fakeNotify{}, &fakeEvents{}
            code, out, diagnostic := run(t, o, append([]string{"--lang", language}, args...)...)
            if code != 0 || out == "" || diagnostic != "" {
                t.Fatal(args, code, out, diagnostic)
            }
        }
        for _, args := range [][]string{{"machine", "add"}, {"machine", "use"}, {"machine", "show"}, {"machine", "remove"}, {"cloud", "acquire"}, {"run"}, {"sync"}} {
            o, _, _, _ := uxCFixture(t, "")
            o.Syncer = &fakeSyncer{}
            o.Interactive = true
            o.In = uxCoreNoInput{t}
            code, out, diagnostic := run(t, o, append([]string{"--lang", language}, args...)...)
            if code != 2 || out != "" || diagnostic == "" {
                t.Fatal("missing inputs did not fail finitely", args, code, out, diagnostic)
            }
        }
    }
}

func TestUXCoreExplicitModeValidationIsOffline(t *testing.T) {
    for _, interactive := range []bool{false, true} {
        o := fixture(t)
        o.Interactive, o.In = interactive, uxCoreNoInput{t}
        code, out, stderr := run(t, o, "-i", "--json")
        var value envelope
        if code != 2 || stderr != "" || json.Unmarshal([]byte(out), &value) != nil || value.Error == nil || value.Error.Code != "BAD_CONFIG" || value.Error.Argument != "--interactive/--json" || value.Error.Reason != "conflicting_arguments" {
            t.Fatal(code, out, stderr)
        }
        if !interactive {
            for _, args := range [][]string{{"-i"}, {"cloud", "--interactive"}, {"machine", "add", "-i"}} {
                code, out, stderr := run(t, o, args...)
                if code != 2 || out != "" || !strings.Contains(stderr, "--interactive") {
                    t.Fatal(args, code, out, stderr)
                }
            }
        }
        for _, args := range [][]string{{"-i", "--help"}, {"help", "cloud", "-i", "--json"}, {"version", "-i", "--json"}, {"-i", "--version"}} {
            if code, out, stderr := run(t, o, args...); code != 0 || out == "" || stderr != "" {
                t.Fatal("help/version depended on UI capability", args, code, out, stderr)
            }
        }
    }
}

func TestUXCoreCompleteTerminalRunStaysDirectAndPreservesProgramFlags(t *testing.T) {
    o, _, _, remote := uxCFixture(t, "target='lab'\n")
    checker := &fakeChecker{}
    o.Runs.Check = checker
    o.Interactive, o.In = true, uxCoreNoInput{t}
    code, out, stderr := run(t, o, "run", "--wait", "0s", "--", "echo", "-i", "--interactive", "--json")
    if code != 0 || out == "" || checker.Batch || remote.launches != 1 || len(remote.req.Args) != 4 || strings.Join(remote.req.Args, "|") != "echo|-i|--interactive|--json" {
        t.Fatal(code, out, stderr, remote.req.Args, remote.launches)
    }
}

func TestUXCoreTerminalWithoutOptInCannotApproveCloudCreation(t *testing.T) {
    o, provider := uxFixture(t)
    o.Interactive, o.In = true, uxCoreNoInput{t}
    code, out, diagnostic := run(t, o, "cloud", "acquire", "gpu", "--image", "base-1", "--gpu-model", "RTX 4090", "--max-compute-hourly-price", "3.00")
    if code != 2 || provider.creates != 0 || !strings.Contains(out+diagnostic, "--yes") {
        t.Fatal("terminal capability granted approval or opened a form", code, out, diagnostic, provider.creates)
    }
}
