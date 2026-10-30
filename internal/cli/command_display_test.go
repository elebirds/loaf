package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "reflect"
    "strings"
    "testing"

    "github.com/alecthomas/kong"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "mvdan.cc/sh/v3/shell"
)

func TestUXJHelpGroupsFollowGrammarAndKeepEveryFlag(t *testing.T) {
    model := &struct {
        JSON bool `help:"flag_json"`
        Demo struct {
            Advanced bool `help:"flag_trace" ux-group:"advanced"`
            Common   bool `help:"flag_dry_run" ux-group:"common"`
            Filter   bool `help:"flag_run_tag" ux-group:"conditions"`
        }   `cmd:"" help:"run_short" example:"loaf demo"`
    }{}
    parser, err := kong.New(model, kong.Name("loaf"), kong.NoDefaultHelp())
    if err != nil {
        t.Fatal(err)
    }
    for _, language := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(language, nil)
        h := helpData(parser.Model.Children[0], l)
        previous := -1
        for _, text := range []string{l.Text("help_examples"), "  loaf demo", l.Text("cloud_help_common"), "--common", l.Text("cloud_help_conditions"), "--filter", l.Text("cloud_help_advanced"), "--advanced", l.Text("cloud_help_global"), "--json"} {
            next := strings.Index(h.Help[previous+1:], text)
            if next < 0 {
                t.Fatalf("%s: missing or unordered %q:\n%s", language, text, h.Help)
            }
            previous += next + 1
        }
        actual, _ := newParser(&grammar{})
        for _, node := range commandNodes(actual.Model.Node) {
            h := helpData(node, l)
            for _, flag := range h.Flags {
                // Descriptions/examples can mention another option; count
                // option rows rather than arbitrary text occurrences.
                count := 0
                for _, line := range strings.Split(h.Help, "\n") {
                    if strings.HasPrefix(line, "  --"+flag.Name+" ") || strings.HasPrefix(line, "  -") && strings.Contains(line, ", --"+flag.Name+" ") {
                        count++
                    }
                }
                if count != 1 {
                    t.Fatalf("%s %s: option %s shown %d times", language, node.Path(), flag.Name, count)
                }
                if flag.Configurable != (flag.ConfigKey != "") {
                    t.Fatalf("inconsistent provenance: %s %+v", node.Path(), flag)
                }
            }
        }
    }
    unknown := []helpValue{{Name: "known", Group: "common"}, {Name: "future", Group: "future"}}
    if !reflect.DeepEqual(uxJHelpFlags(unknown), unknown) {
        t.Fatal("unknown group hid a flag")
    }
}

func TestUXJCommandDisplayRoundTrips(t *testing.T) {
    values := []string{"", "plain", "seed 1", "双引号\"与'单引号", "#comment", "~/outputs", "a,b", "a=b", "$HOME", "$(no-command)", "`no-command`", "a;false", "a|b", "*?[x]", "{a,b}", "back\\slash", "!history", "-1", "--yes", "--json"}
    for _, value := range values {
        argv := []string{"loaf", "run", "--", "printf", "%s", value}
        text, err := uxJCommandDisplay(argv)
        if err != nil {
            t.Fatal(err)
        }
        read, err := shell.Fields(text, uxJTestEnvironment)
        if err != nil || !reflect.DeepEqual(read, argv) {
            t.Fatalf("argv changed: %q -> %q -> %q (%v)", argv, text, read, err)
        }
    }
    for _, value := range []string{"private\x00value", "private\nvalue", "private\x1b[31m", "private\rvalue", "private\u202evalue", string([]byte{0xff})} {
        text, err := uxJCommandDisplay([]string{"loaf", value})
        if err == nil || text != "" || strings.Contains(err.Error(), "private") {
            t.Fatal("unsafe/unrepresentable display", text, err)
        }
    }
}

func TestUXJHelpExamplesUseSafeDisplayAndRetainHelpOnFailure(t *testing.T) {
    parsed, err := uxJReadCommand([]string{"loaf", "run"})
    if err != nil {
        t.Fatal(err)
    }
    node := parsed.node
    for _, language := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(language, nil)
        h := helpData(node, l)
        argv := []string{"loaf", "run", "--", "printf", "%s", "#literal", "~/literal", "!literal", ""}
        h.Examples = [][]string{argv, {"loaf", "PRIVATE\x1b[31m"}}
        var out strings.Builder
        renderHelp(&out, node, h, l)
        display, err := uxJCommandDisplay(argv)
        if err != nil || !strings.Contains(out.String(), "  "+display+"\n") {
            t.Fatal(out.String(), err)
        }
        read, err := shell.Fields(display, uxJTestEnvironment)
        if err != nil || !reflect.DeepEqual(read, argv) {
            t.Fatal(read, err)
        }
        diagnostic := (&presenter{locale: l, command: "run"}).error(uxJCommandError()).Message
        if !strings.Contains(out.String(), diagnostic) || strings.Contains(out.String(), "PRIVATE") || strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "--dry-run") {
            t.Fatal("unsafe example hid help or leaked input", out.String())
        }
    }
}

func TestUXJCanonicalArgvPreservesLiteralAndExplicitValues(t *testing.T) {
    cases := [][]string{
        {"loaf", "run", "--wait=0s", "--name=种子 1", "--tag=a,b", "--tag=seed1", "--json", "--", "python3", "a b.py", "", "--json", "--rate", "-1"},
        {"loaf", "run", "--", "/bin/sh", "-c", `printf '%s' "$HOME"`},
        {"loaf", "exec", "--cwd=", "--", "printf", "%s", "双引号\"", ""},
        {"loaf", "queue", "submit", "--name=seed 1", "--", "python3", "a b.py", "--yes"},
        {"loaf", "sync", "--dry-run", "--respect-gitignore=false", "--exclude=dir with spaces/", "--exclude=*.tmp"},
        {"loaf", "pull", "r_fixture", "--checksum=false", "--path=outputs/a,b.csv", "--path=outputs/seed 1", "--to=./export dir"},
        {"loaf", "logs", "r_fixture", "--tail=0"},
        {"loaf", "stop", "r_fixture", "--grace=0s"},
        {"loaf", "check", "--targets=" + cloudCSV([]string{"a,b", "c\"d", "中文 空格"})},
        {"loaf", "cloud", "profile", "save", "empty", "--region=", "--gpu-model=", "--min-cpus=0", "--continuous=false", "--wait=0s"},
        {"loaf", "notify", "subscribe", "run:r_fixture", "--destination=research", "--replay=false"},
    }
    for _, argv := range cases {
        first, err := uxJReadCommand(argv)
        if err != nil {
            t.Fatal(argv, err)
        }
        shown, err := uxJCommandDisplay(argv)
        if err != nil {
            t.Fatal(err)
        }
        read, err := shell.Fields(shown, uxJTestEnvironment)
        if err != nil || !reflect.DeepEqual(argv, read) {
            t.Fatal(shown, read, err)
        }
        second, err := uxJReadCommand(read)
        if err != nil || first.path != second.path || !reflect.DeepEqual(first.grammar, second.grammar) {
            t.Fatal("effective parsed input changed", shown, err)
        }
    }
    parsed, _ := uxJReadCommand(cases[9])
    p := parsed.grammar.Cloud.Profile.Save
    if !p.Region.Present || p.Region.Values == nil || len(p.Region.Values) != 0 || !p.GPUModel.Present || p.Continuous == nil || *p.Continuous || p.MinCPUs == nil || *p.MinCPUs != 0 || p.Wait == nil || *p.Wait != "0s" {
        t.Fatal("empty/false/zero became omitted", p)
    }
    parsed, _ = uxJReadCommand(cases[0])
    args, err := commandArgs(parsed.grammar.Run.Argv, false)
    if err != nil || !reflect.DeepEqual(args, []string{"python3", "a b.py", "", "--json", "--rate", "-1"}) {
        t.Fatal(args, err)
    }
}

func TestUXJNextActionCopiesPublicArgvWithoutApproval(t *testing.T) {
    argv := []string{"loaf", "clean", "r_frozen", "--dry-run"}
    action, err := uxJNextAction("preview_cleanup", argv, false)
    if err != nil || action.Kind != "preview_cleanup" || action.RequiresConfirmation {
        t.Fatal(action, err)
    }
    argv[2] = "r_other"
    if action.Command[2] != "r_frozen" {
        t.Fatal("action aliased caller memory")
    }
    raw, _ := json.Marshal(action)
    var shape map[string]any
    if json.Unmarshal(raw, &shape) != nil || len(shape) != 3 || shape["kind"] != "preview_cleanup" || shape["command"] == nil || shape["requires_confirmation"] != false {
        t.Fatal(string(raw))
    }
    for _, argv := range [][]string{
        {"loaf", "clean", "r_frozen", "--yes"}, {"loaf", "clean", "r_frozen", "-y"}, {"loaf", "clean", "r_frozen", "--yes=false"},
        {"loaf", "clean", "r_frozen", "--help", "--yes"},
        {"loaf", "cloud", "credentials", "instance", "--show-password"},
        {"loaf", "run", "python3", "train.py"}, {"loaf", "exec", "--"},
        {"loaf", "logs", "r_frozen", "--target=other"}, {"loaf", "machine", "list", "--dry-run"},
        {"loaf", "ssh", "--json"}, {"loaf", "forward", "6006", "--json"},
        {"loaf", "notify", "channel", "--socket=/tmp/fixture.sock", "--session=fixture", "--json"},
        {"loaf", "private-unknown-command"}, {"loaf", "inspect"},
    } {
        a, err := uxJNextAction("inspect", argv, false)
        if err == nil || len(a.Command) != 0 || strings.Contains(err.Error(), "private") {
            t.Fatal("invalid action exported", argv, a, err)
        }
    }
    // The literal program option is not Loaf payment/deletion approval.
    if _, err := uxJNextAction("run_public_input", []string{"loaf", "run", "--", "program", "--yes"}, true); err != nil {
        t.Fatal(err)
    }
}

// Domain handoffs supply these public inputs. This checks their syntax and
// output-mode equivalence, not remote existence or permission to execute them.
func TestUXJDeliveredDomainCommandsRoundTrip(t *testing.T) {
    cases := []struct {
        domain string
        argv   []string
        native bool
    }{
        {"A", []string{"loaf", "machine", "show", "fixture"}, false},
        {"B", []string{"loaf", "check", "--target", "fixture"}, false},
        {"B-native", []string{"loaf", "ssh", "--target", "fixture"}, true},
        {"B-forward", []string{"loaf", "forward", "6006", "--local-port", "16006", "--target", "fixture"}, true},
        {"C", []string{"loaf", "run", "--dry-run", "--", "python3", "train.py"}, false},
        {"D", []string{"loaf", "inspect", "r_fixture"}, false},
        {"E", []string{"loaf", "logs", "r_fixture", "-f", "--tail=0"}, false},
        {"F", []string{"loaf", "pull", "r_fixture", "--path", "outputs/模型 [1]*?,.txt", "--to", "./review copy", "--checksum=false"}, false},
        {"F-retry", []string{"loaf", "pull", "r_fixture", "--into", "./comparison"}, false},
        {"G", []string{"loaf", "clean", "r_one", "r_two", "--dry-run"}, false},
        {"H", []string{"loaf", "queue", "show", "q_fixture"}, false},
        {"I", []string{"loaf", "notify", "delivery", "show", "d_fixture"}, false},
    }
    for _, tc := range cases {
        t.Run(tc.domain, func(t *testing.T) {
            direct, err := uxJReadCommand(tc.argv)
            if err != nil {
                t.Fatal(err)
            }
            shown, err := uxJCommandDisplay(tc.argv)
            if err != nil {
                t.Fatal(err)
            }
            read, err := shell.Fields(shown, uxJTestEnvironment)
            if err != nil || !reflect.DeepEqual(read, tc.argv) {
                t.Fatal(shown, read, err)
            }
            // Insert Loaf's output mode before any program-argument separator.
            structured := append([]string{"loaf", "--json"}, read[1:]...)
            parsed, err := uxJReadCommand(structured)
            if tc.native {
                if err == nil {
                    t.Fatal("native command accepted JSON")
                }
                return
            }
            if err != nil || parsed.path != direct.path || !parsed.grammar.JSON {
                t.Fatal(parsed.path, err)
            }
            parsed.grammar.JSON = false
            if !reflect.DeepEqual(parsed.grammar, direct.grammar) {
                t.Fatal("output mode changed the request")
            }
        })
    }
}

func uxJTestEnvironment(name string) string {
    switch name {
    case "IFS":
        return " \t\n"
    case "PWD":
        return "/fixture"
    default:
        return "EXPANSION_MUST_NOT_APPEAR"
    }
}

func TestUXJLocalIssuesKeepPrimaryErrorAndRejectValues(t *testing.T) {
    parsed, err := uxJReadCommand([]string{"loaf", "cloud", "acquire"})
    if err != nil {
        t.Fatal(err)
    }
    original := []inputIssue{{"--image", "missing_argument"}, {"--max-compute-hourly-price", "missing_argument"}, {"--image", "missing_argument"}}
    issues, err := uxJInputIssues(parsed.node, original...)
    if err != nil || !reflect.DeepEqual(issues, original[:2]) {
        t.Fatal(issues, err)
    }
    for _, language := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(language, nil)
        p := presenter{locale: l, command: "cloud acquire", issues: issues}
        failure := fault.Invalid("--image", "missing_argument", "cloud_image_required")
        v := p.error(failure)
        if v.Code != "BAD_CONFIG" || exitCode(v) != 2 || v.Argument != "--image" || v.Reason != "missing_argument" || !reflect.DeepEqual(v.Issues, issues) || !reflect.DeepEqual(v.HelpCommand, []string{"loaf", "help", "cloud", "acquire"}) {
            t.Fatal(v)
        }
    }
    for _, issue := range []inputIssue{{"--image=PRIVATE", "missing_argument"}, {"PRIVATE", "invalid_value"}, {"--image", "PRIVATE"}} {
        got, err := uxJInputIssues(parsed.node, issue)
        if err == nil || got != nil || strings.Contains(err.Error(), "PRIVATE") {
            t.Fatal(got, err)
        }
    }
    logCommand, _ := uxJReadCommand([]string{"loaf", "logs", "r_fixture"})
    normalized, err := uxJInputIssues(logCommand.node,
        inputIssue{"-f/--tail", "conflicting_arguments"},
        inputIssue{"--follow/--tail", "conflicting_arguments"},
        inputIssue{"run-id", "invalid_value"})
    if err != nil || !reflect.DeepEqual(normalized, []inputIssue{{"--follow/--tail", "conflicting_arguments"}, {"run-id", "invalid_value"}}) {
        t.Fatal(normalized, err)
    }
    params := map[string]any{"Value": "int"}
    before := map[string]any{"Value": "int"}
    first := inputFailure("invalid_type", "--limit", "input_invalid_type", params, parsed.node)
    second := inputFailure("invalid_type", "--tail", "input_invalid_type", params, parsed.node)
    if !reflect.DeepEqual(params, before) || first.Params["Argument"] != "--limit" || second.Params["Argument"] != "--tail" {
        t.Fatal("diagnostics share mutable caller parameters", params, first, second)
    }
}

type uxJNoInput struct{ t *testing.T }

func (r uxJNoInput) Read([]byte) (int, error) {
    r.t.Fatal("noninteractive/help invocation read stdin")
    return 0, io.EOF
}

func uxJEnvelope(t *testing.T, out string) envelope {
    t.Helper()
    decoder := json.NewDecoder(strings.NewReader(out))
    var result envelope
    if err := decoder.Decode(&result); err != nil || result.SchemaVersion != 1 {
        t.Fatal(out, err)
    }
    var extra any
    if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
        t.Fatal("stdout contains more than one result", out, err)
    }
    return result
}

func TestUXJEachDomainLocalErrorsNeverPromptOrWriteState(t *testing.T) {
    tests := []struct {
        domain string
        args   []string
    }{
        {"A", []string{"machine", "add", "fixture"}},
        {"B", []string{"exec", "--"}},
        {"C", []string{"run", "--wait=PRIVATE", "--", "program"}},
        {"D", []string{"status", "--limit=0"}},
        {"E", []string{"logs", "--tail=-1"}},
        {"F", []string{"pull", "r_one", "r_two"}},
        {"G", []string{"clean"}},
        {"H", []string{"queue", "submit", "program"}},
        {"I", []string{"notify", "destination", "add", "fixture"}},
    }
    for _, tc := range tests {
        for _, language := range []string{"en", "zh-Hans"} {
            for _, structured := range []bool{false, true} {
                t.Run(tc.domain+"/"+language+"/"+map[bool]string{false: "pipe", true: "json"}[structured], func(t *testing.T) {
                    o := fixture(t)
                    o.Runs = &execution.Service{Index: o.State}
                    o.In = uxJNoInput{t}
                    o.Interactive = structured // JSON must also override TTY.
                    o.Queue = &fakeQueue{}
                    o.Notify = &fakeNotify{}
                    args := []string{"--lang", language}
                    if structured {
                        args = append(args, "--json")
                    }
                    args = append(args, tc.args...)
                    code, out, stderr := run(t, o, args...)
                    if code != 2 || strings.Contains(out+stderr, "PRIVATE") {
                        t.Fatal(args, code, out, stderr)
                    }
                    if structured {
                        e := uxJEnvelope(t, out)
                        if stderr != "" || e.OK || e.Error == nil || e.Error.Code != "BAD_CONFIG" || len(e.Error.HelpCommand) == 0 {
                            t.Fatal(out, stderr)
                        }
                    } else if out != "" || stderr == "" {
                        t.Fatal(out, stderr)
                    }
                    for _, path := range []string{o.Config.ConfigFile, o.State.Path} {
                        if _, err := os.Stat(path); !os.IsNotExist(err) {
                            t.Fatal("local input error wrote state", path, err)
                        }
                    }
                })
            }
        }
    }
}

func TestUXJHelpAndVersionRemainOffline(t *testing.T) {
    for _, args := range [][]string{{"--help"}, {"--json"}, {"help", "run", "--json"}, {"version", "--json"}, {"--version", "--json"}, {"help", "notify", "channel", "--json"}} {
        var out, diagnostic bytes.Buffer
        code := Execute(context.Background(), args, Options{In: uxJNoInput{t}, Out: &out, Err: &diagnostic, Interactive: true, InitError: errors.New("PRIVATE"), Language: "invalid-personal-language", Env: map[string]string{"LANG": "C"}})
        if code != 0 || diagnostic.Len() != 0 || strings.Contains(out.String(), "PRIVATE") {
            t.Fatal(args, code, out.String(), diagnostic.String())
        }
        if strings.Contains(strings.Join(args, " "), "--json") {
            uxJEnvelope(t, out.String())
        }
    }
}
