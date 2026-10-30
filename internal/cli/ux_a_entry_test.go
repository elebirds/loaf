package cli

import (
    "context"
    "encoding/json"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/client"
)

// These entry checks require 00's machine routing and catalog integration.
// They intentionally fail rather than silently skipping an unwired component.
func TestUXAEntryBareAndMissingInputs(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        o.Interactive = true
        o.In = strings.NewReader("4\n")
        code, out, stderr := run(t, o, "-i", "--lang", language, "machine")
        if code != 0 || out != "" || !strings.Contains(stderr, "1.") {
            t.Fatal("bare interactive machine is not wired to its hub", code, out, stderr)
        }
        o.In = strings.NewReader("lab\nlab-host\n1\n")
        if code, out, stderr := run(t, o, "-i", "--lang", language, "machine", "add"); code != 0 {
            t.Fatal("missing-name add did not use its wizard", code, out, stderr)
        }
        o.In = strings.NewReader("1\n")
        if code, out, stderr := run(t, o, "-i", "--lang", language, "machine", "use"); code != 0 {
            t.Fatal("missing-name use did not use A's picker", code, out, stderr)
        }
        if code, out, stderr := run(t, o, "--lang", language, "machine", "list", "--json"); code != 0 || !strings.Contains(out, `"target":"lab"`) || stderr != "" {
            t.Fatal(code, out, stderr)
        }
    }
}

func TestUXAEntryDirectJSONAndNoninteractiveContracts(t *testing.T) {
    for _, interactive := range []bool{false, true} {
        o := fixture(t)
        o.Interactive, o.In = interactive, uxANoInput{t}
        for _, args := range [][]string{
            {"machine", "add", "lab", "--ssh", "lab-host", "--json"},
            {"machine", "use", "lab", "--json"}, {"machine", "show", "lab", "--json"},
            {"machine", "list", "--json"}, {"machine", "--json"},
            {"machine", "remove", "lab", "--json"},
        } {
            code, out, stderr := run(t, o, args...)
            var value envelope
            if err := json.Unmarshal([]byte(out), &value); err != nil || code != 0 || stderr != "" || !value.OK {
                t.Fatal(args, code, out, stderr, err)
            }
        }
        for _, path := range []string{"add", "use", "show", "remove"} {
            code, out, stderr := run(t, o, "machine", path, "--json")
            var value envelope
            if err := json.Unmarshal([]byte(out), &value); err != nil || code != 2 || stderr != "" || value.Error == nil || value.Error.Reason != "missing_argument" || value.Error.Argument != "name" {
                t.Fatal(path, code, out, stderr, err)
            }
            if !interactive {
                if code, out, stderr := run(t, o, "machine", path); code != 2 || out != "" || stderr == "" {
                    t.Fatal(path, code, out, stderr)
                }
            }
        }
    }
}

func TestUXAEntryDoesNotRelaxCloudNameRequirements(t *testing.T) {
    for _, command := range [][]string{{"cloud", "account", "remove"}, {"cloud", "profile", "show"}} {
        parser, err := newParser(&grammar{})
        if err != nil {
            t.Fatal(err)
        }
        if _, err := parser.Parse(command); err == nil {
            t.Fatal("machine optional-name wiring changed an unrelated Cloud command", command)
        }
    }
}

func TestUXAEntryBConsumesPickerWithoutPersistingSelection(t *testing.T) {
    for _, cancel := range []bool{false, true} {
        keys := "2\n"
        if cancel {
            keys = "5\n"
        }
        c := uxATestCall(t, keys, "en", "alpha", "beta")
        operations := &fakeOperations{}
        c.app.Machines.Operations = operations
        if _, err := c.app.Machines.UseTarget(context.Background(), "alpha"); err != nil {
            t.Fatal(err)
        }
        _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "ssh"})
        if cancel {
            if !uxACanceled(err) || operations.calls != 0 {
                t.Fatal("canceled B selection opened a connection", err, operations.calls)
            }
        } else if err != nil || operations.calls != 1 || operations.target.Destination != "beta-host" {
            t.Fatal("B did not consume A's exact temporary choice", err, operations.calls, operations.target)
        }
        selected, err := c.app.Machines.Targets(context.Background(), "")
        if err != nil || selected.Project.Target != "alpha" {
            t.Fatal("temporary B selection changed the project", selected, err)
        }
    }
}

func TestUXAEntryNavigationUsesRealHandlers(t *testing.T) {
    c := uxATestCall(t, "lab\nlab-host\n1\n4\n", "en")
    for _, kind := range []string{"machine", "machine-add"} {
        if !c.uxRouteAvailable(kind) {
            t.Fatalf("00 has not consumed route %s", kind)
        }
    }
    data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "machine-add"})
    if err != nil || data.(client.AddResult).Target != "lab" {
        t.Fatal(data, err, c.options.Err)
    }
    // A second child sees the refreshed application snapshot immediately.
    selected, err := c.uxAPickTarget(context.Background(), "")
    if selected != "" || !uxACanceled(err) {
        t.Fatal(selected, err)
    }
}

func TestUXAEntryPlainTerminalModes(t *testing.T) {
    for _, mode := range []string{"NO_COLOR", "TERM=dumb"} {
        t.Run(mode, func(t *testing.T) {
            c, master, output, ctx := uxAOpenTerminal(t, "zh-Hans", 80, 18, false)
            options := c.options
            options.App, options.TerminalError, options.TerminalOutput = c.app, true, true
            options.Env = map[string]string{"TERM": "xterm-256color"}
            if mode == "NO_COLOR" {
                options.Env["NO_COLOR"] = "1"
            } else {
                options.Env["TERM"] = "dumb"
            }
            done, joined := make(chan int, 1), make(chan struct{})
            go func() {
                defer close(joined)
                done <- Execute(ctx, []string{"-i", "--lang", "zh-Hans", "machine"}, options)
            }()
            t.Cleanup(func() { output.cancel(); <-joined })
            uxAWaitTerminal(t, ctx, output, 0, "> ")
            uxACaptureTerminal(t, output, "entry-"+mode+"-80x18")
            uxAKeys(t, master, "4\n", output)
            if code := <-done; code != 0 || strings.Contains(output.raw(), "\x1b[") {
                t.Fatal(code, output.raw())
            }
        })
    }
}
