package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
)

func uxCoreCall(t *testing.T, input string) *invocation {
    t.Helper()
    o := fixture(t)
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l := i18n.English()
    return &invocation{ui: true,
        app: client.New(o.Dependencies, settings), options: o.Options, grammar: &grammar{},
        output: &presenter{locale: l, out: o.Out, err: o.Err},
        input:  interaction.New(interaction.Options{Interactive: true, In: strings.NewReader(input), Err: o.Err}),
    }
}

func uxCoreRootChoice(t *testing.T, o testOptions, kind string) int {
    t.Helper()
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    c := &invocation{ui: true, app: client.New(o.Dependencies, settings), output: &presenter{locale: i18n.English()}}
    for i, choice := range c.uxRootChoices() {
        if choice.Value == kind {
            return i + 1
        }
    }
    t.Fatal("missing integrated root choice", kind)
    return 0
}

func TestUXCoreRootOverviewIsLocalAndLeavesNoSelection(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        o.Interactive = true
        o.In = strings.NewReader(strconv.Itoa(uxCoreRootChoice(t, o, "exit")) + "\n")
        if code, _, stderr := run(t, o, "machine", "add", "lab", "--ssh", "lab", "--json"); code != 0 {
            t.Fatal(code, stderr)
        }
        if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("target = 'lab'\n"), 0600); err != nil {
            t.Fatal(err)
        }
        before, err := os.ReadFile(o.Config.ConfigFile)
        if err != nil {
            t.Fatal(err)
        }
        code, out, stderr := run(t, o, "-i", "--lang", language)
        l, _ := i18n.New(language, nil)
        if code != 0 || out != "" || !strings.Contains(stderr, "Loaf") || !strings.Contains(stderr, "lab") || !strings.Contains(stderr, filepath.Base(o.CWD)) {
            t.Fatal(language, code, out, stderr)
        }
        for _, id := range []string{"ux_core_run", "ux_core_tasks", "ux_core_machines", "ux_core_cloud", "ux_core_more"} {
            if !strings.Contains(stderr, l.Text(id)) {
                t.Fatal("integrated domain missing", id, stderr)
            }
        }
        if strings.Contains(stderr, l.Text("ux_core_results")) {
            t.Fatal("unavailable results service was advertised", stderr)
        }
        after, err := os.ReadFile(o.Config.ConfigFile)
        if err != nil || !bytes.Equal(before, after) {
            t.Fatal("overview changed user settings", err)
        }
        if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
            t.Fatal("overview created project state", err)
        }
    }
}

func TestUXCoreRootHelpAndJSONNeverReadInput(t *testing.T) {
    for _, args := range [][]string{nil, {"--json"}, {"help"}, {"--help"}, {"help", "cloud"}, {"cloud", "--help"}} {
        for _, interactive := range []bool{false, true} {
            if len(args) == 0 && interactive {
                continue
            }
            o := fixture(t)
            o.Interactive = interactive
            o.In = uxCoreNoInput{t}
            code, out, stderr := run(t, o, args...)
            if code != 0 || out == "" || stderr != "" {
                t.Fatal(args, interactive, code, out, stderr)
            }
            if len(args) == 1 && args[0] == "--json" {
                var value envelope
                decoder := json.NewDecoder(strings.NewReader(out))
                if err := decoder.Decode(&value); err != nil || !value.OK {
                    t.Fatal(out, err)
                }
                if err := decoder.Decode(&value); !errors.Is(err, io.EOF) {
                    t.Fatal("more than one stdout result", out, err)
                }
            }
            if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
                t.Fatal("help created state", err)
            }
        }
    }
}

type uxCoreNoInput struct{ t *testing.T }

func (r uxCoreNoInput) Read([]byte) (int, error) {
    r.t.Error("offline help attempted to prompt")
    return 0, io.EOF
}

func TestUXCoreRootCloudRoundTripHasNoDefaultWrites(t *testing.T) {
    o := fixture(t)
    o.Interactive = true
    o.In = strings.NewReader(strconv.Itoa(uxCoreRootChoice(t, o, "cloud")) + "\n5\n" + strconv.Itoa(uxCoreRootChoice(t, o, "exit")) + "\n")
    code, out, stderr := run(t, o, "-i", "--target", "temporary")
    if code != 0 || out != "" || !strings.Contains(stderr, i18n.English().Text("cloud_hub_title")) || strings.Count(stderr, "Loaf") != 2 {
        t.Fatal(code, out, stderr)
    }
    if _, err := os.Stat(o.Config.ConfigFile); !os.IsNotExist(err) {
        t.Fatal("navigation wrote defaults", err)
    }
    if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
        t.Fatal("navigation created state", err)
    }
}

func TestUXCoreChildUsesGrammarDefaultsAndIsolatesState(t *testing.T) {
    c := uxCoreCall(t, "")
    name := "original"
    c.grammar.Target = &name
    c.output.command = "parent"
    c.output.nextActions = []cloudNextAction{{Kind: "parent", Command: []string{"loaf", "inspect", "r_parent"}}}
    child, err := c.uxChild("run")
    if err != nil || child.grammar == c.grammar || child.output == c.output || child.input != c.input || child.app != c.app {
        t.Fatal(child, err)
    }
    if child.grammar.Target == nil || *child.grammar.Target != name || child.grammar.Target == c.grammar.Target || child.output.command != "run" || len(child.output.nextActions) != 0 {
        t.Fatal(child.grammar.Target, child.output)
    }
    *child.grammar.Target = "changed"
    child.output.command = "changed"
    child.output.nextActions = append(child.output.nextActions, cloudNextAction{Kind: "child"})
    if name != "original" || c.output.command != "parent" || c.output.nextActions[0].Kind != "parent" {
        t.Fatal("child mutated parent")
    }
    child, err = c.uxChild("cloud", "list")
    if err != nil || child.grammar.Target != nil || child.grammar.Cloud.List.Limit != 100 {
        t.Fatal(child, err)
    }
}

func TestUXCoreRoutesRejectRedirectionBeforeApplicationCalls(t *testing.T) {
    target := "replacement"
    for _, route := range []uxRoute{
        {Kind: "logs"}, {Kind: "task", ID: "r_saved", Target: &target},
        {Kind: "outputs", ID: "r_saved", IDs: []string{"r_other"}},
        {Kind: "notify-run", ID: "r_saved", Target: &target},
        {Kind: "clean", IDs: []string{"r_saved"}, Target: &target},
        {Kind: "cloud", ID: "r_saved"}, {Kind: "cloud", Target: &target}, {Kind: "not-a-route"},
    } {
        c := &invocation{}
        _, err := c.uxNavigate(context.Background(), route)
        var f *fault.Error
        if !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
            t.Fatal(route, err)
        }
    }
    c := &invocation{}
    if _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "tasks"}); err == nil {
        t.Fatal("unintegrated route returned success")
    }
    c.policy.Batch = true
    if _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "cloud"}); !errors.Is(err, interaction.ErrNonInteractive) {
        t.Fatal(err)
    }
}

func TestUXCoreDocumentPagesAndBatchStaySeparate(t *testing.T) {
    c := uxCoreCall(t, "1\n3\n")
    if err := c.uxShowDocument(context.Background(), interaction.Document{Title: "Read details", Body: strings.Repeat("中文 result row\n", 70)}); err != nil {
        t.Fatal(err)
    }
    text := c.options.Err.(*bytes.Buffer).String()
    if !strings.Contains(text, "Page 1 of") || !strings.Contains(text, "Page 2 of") || !strings.Contains(text, "中文") || c.options.Out.(*bytes.Buffer).Len() != 0 {
        t.Fatal(text)
    }
    c.policy.Batch = true
    before := text
    if err := c.uxShowDocument(context.Background(), interaction.Document{Title: "must not print"}); !errors.Is(err, interaction.ErrNonInteractive) || before != c.options.Err.(*bytes.Buffer).String() {
        t.Fatal(err)
    }
}

func TestUXCoreBareMachineQueueNotifyRetainNoninteractiveDefaults(t *testing.T) {
    for _, args := range [][]string{{"machine", "--json"}, {"queue", "--json"}, {"notify", "--json"}} {
        o := fixture(t)
        o.Interactive = true
        o.In = uxCoreNoInput{t}
        _, out, stderr := run(t, o, args...)
        var value map[string]any
        if err := json.Unmarshal([]byte(out), &value); err != nil || stderr != "" {
            t.Fatal(args, out, stderr, err)
        }
        data, _ := value["data"].(map[string]any)
        if _, help := data["help"]; help {
            t.Fatal("bare command was redirected to help", args, out)
        }
    }
}

func TestUXCoreTraceAndNoColorDoNotStartAConflictingRenderer(t *testing.T) {
    o := fixture(t)
    o.Interactive = true
    o.In = uxCoreNoInput{t}
    code, _, stderr := run(t, o, "-i", "--trace")
    if code != 2 || !strings.Contains(stderr, i18n.English().Text("trace_finite_only")) {
        t.Fatal(code, stderr)
    }
    o.Env["NO_COLOR"] = "1"
    o.Env["TERM"] = "xterm-256color"
    o.TerminalOutput, o.TerminalError = true, true
    o.In = strings.NewReader(strconv.Itoa(uxCoreRootChoice(t, o, "exit")) + "\n")
    code, out, stderr := run(t, o, "-i")
    if code != 0 || out != "" || strings.Contains(stderr, "\x1b[") {
        t.Fatal(code, out, stderr)
    }
}
