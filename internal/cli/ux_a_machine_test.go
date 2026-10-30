package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
)

func uxATestCall(t *testing.T, input, language string, names ...string) *invocation {
    t.Helper()
    o := fixture(t)
    for _, name := range names {
        if err := o.Config.Add(context.Background(), name, config.Target{Destination: name + "-host", RemoteBase: "~/projects"}, false); err != nil {
            t.Fatal(err)
        }
    }
    return uxATestFrom(t, o, input, language)
}

func uxATestFrom(t *testing.T, o testOptions, input, language string) *invocation {
    t.Helper()
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l, err := i18n.New(language, nil)
    if err != nil {
        t.Fatal(err)
    }
    model := &grammar{}
    parser, err := newParser(model)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := parser.Parse([]string{"machine", "list"}); err != nil {
        t.Fatal(err)
    }
    // No remote capabilities are needed for machine management.
    o.Checker, o.Runner, o.Operations, o.Cloud, o.Auth = nil, nil, nil, nil, nil
    o.Interactive = true
    o.In = strings.NewReader(input)
    return &invocation{ui: true,
        app: client.New(o.Dependencies, settings), options: o.Options, grammar: model,
        output: &presenter{locale: l, out: o.Out, err: o.Err},
        input:  interaction.New(interaction.Options{In: o.In, Err: o.Err, Interactive: true, ChoiceError: l.Text("ux_core_choice_invalid")}),
    }
}

func TestUXAPickerSelectsRealAliasWithoutChangingProject(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        c := uxATestCall(t, "2\n", language, "alpha", "beta")
        if _, err := c.app.Machines.UseTarget(context.Background(), "alpha"); err != nil {
            t.Fatal(err)
        }
        before, err := c.app.Machines.Targets(context.Background(), "")
        if err != nil {
            t.Fatal(err)
        }
        target, err := c.uxAPickTarget(context.Background(), "")
        after, readErr := c.app.Machines.Targets(context.Background(), "")
        if err != nil || readErr != nil || target != "beta" || !reflect.DeepEqual(before, after) {
            t.Fatal(target, err, before, after, readErr)
        }
        if text := c.options.Err.(*bytes.Buffer).String(); !strings.Contains(text, "* SSH · alpha") || !strings.Contains(text, c.output.locale.Text("ux_a_pick")) || strings.Contains(text, "<no value>") {
            t.Fatal(text)
        }
        if c.options.Out.(*bytes.Buffer).Len() != 0 {
            t.Fatal("picker wrote protocol stdout")
        }
    }
}

func TestUXAPickerCancelNeverSelectsFirstAndKeepsLocalReadsEmpty(t *testing.T) {
    for _, names := range [][]string{nil, {"alpha", "beta"}} {
        for _, input := range []string{"\n", ""} {
            c := uxATestCall(t, input, "en", names...)
            target, err := c.uxAPickTarget(context.Background(), "missing")
            if target != "" || !uxACanceled(err) {
                t.Fatal(target, err)
            }
            if _, err := os.Stat(c.app.Machines.State.Path); !os.IsNotExist(err) {
                t.Fatal("picker created project state", err)
            }
            if len(names) == 0 {
                if _, err := os.Stat(filepath.Dir(c.app.Machines.Config.ConfigFile)); !os.IsNotExist(err) {
                    t.Fatal("empty picker created settings", err)
                }
                if !strings.Contains(c.options.Err.(*bytes.Buffer).String(), c.output.locale.Text("ux_a_manage")) {
                    t.Fatal("empty picker lacks management entry")
                }
            }
        }
    }
}

func TestUXAPickerSearchNoMatchesAndClear(t *testing.T) {
    c := uxATestCall(t, "3\nno-match\n1\n\n2\n", "zh-Hans", "alpha", "beta")
    target, err := c.uxAPickTarget(context.Background(), "")
    if err != nil || target != "beta" || !strings.Contains(c.options.Err.(*bytes.Buffer).String(), c.output.locale.Text("ux_a_no_matches")) {
        t.Fatal(target, err, c.options.Err)
    }
}

func TestUXAPickerManageAddReturnsFreshAliasWithoutSelection(t *testing.T) {
    c := uxATestCall(t, "2\n3\nlab\nlab-host\n1\n5\n1\n", "en")
    target, err := c.uxAPickTarget(context.Background(), "")
    if err != nil || target != "lab" {
        t.Fatal(target, err, c.options.Err)
    }
    for _, settings := range []config.Global{c.app.Machines.Settings, c.app.Projects.Settings, c.app.Resources.Settings} {
        if settings.Targets["lab"].Destination != "lab-host" {
            t.Fatal("saved alias missing from app snapshot", settings.Targets)
        }
    }
    if _, err := os.Stat(c.app.Machines.State.Path); !os.IsNotExist(err) {
        t.Fatal("registration selected a project machine", err)
    }
}

func TestUXAAddEditsKeepDraftAndValidateLocalInputs(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        input := "中文别名\n" + strings.Repeat("a", 65) + "\nlab\nssh host command\nssh host | cat\nssh host > file\nssh -J jump -p 2222 user@host\n3\n/\n~/实验\n2\n\n\n1\n"
        c := uxATestCall(t, input, language)
        result, err := c.uxAMachineAdd(context.Background())
        if err != nil {
            t.Fatal(result, err, c.options.Err)
        }
        saved, err := c.app.Machines.Config.Load()
        target := saved.Targets["lab"]
        if err != nil || target.Destination != "user@host" || target.RemoteBase != "~/实验" || !reflect.DeepEqual(target.Args, []string{"-J", "jump", "-p", "2222"}) {
            t.Fatal(target, err, c.options.Err)
        }
        text := c.options.Err.(*bytes.Buffer).String()
        if !strings.Contains(text, c.output.locale.Text("ux_a_name_invalid")) || !strings.Contains(text, c.output.locale.Text("ux_a_remote_invalid")) || strings.Count(text, c.output.locale.Text("ssh_input")) != 3 {
            t.Fatal(text)
        }
    }
}

func TestUXAConflictRequiresExplicitReplacementAndShowsScope(t *testing.T) {
    for _, accept := range []bool{false, true} {
        decision := "5"
        if accept {
            decision = "1"
        }
        c := uxATestCall(t, "lab\nnew-host\n\n"+decision+"\n", "en", "lab", "other")
        c.grammar.Machine.Add.Replace = true
        result, err := c.uxAMachineAdd(context.Background())
        if accept && err != nil || !accept && !errors.Is(err, interaction.ErrCanceled) {
            t.Fatal(result, err, c.options.Err)
        }
        settings, err := c.app.Machines.Config.Load()
        expected := "lab-host"
        if accept {
            expected = "new-host"
        }
        if err != nil || settings.Targets["lab"].Destination != expected || settings.Targets["other"].Destination != "other-host" {
            t.Fatal(settings.Targets, err)
        }
        output := c.options.Err.(*bytes.Buffer).String()
        output = strings.Join(strings.Fields(output), " ")
        for _, required := range []string{"Existing alias", "lab-host", "new-host", "Replace the existing alias?", "Historical tasks keep their original bindings."} {
            if !strings.Contains(output, required) {
                t.Fatal("replacement review missing", required, output)
            }
        }
    }
}

func TestUXASelectedRemovalKeepsBindingsAndOffersChoice(t *testing.T) {
    o, _ := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "machine", "add", "zeta", "--ssh", "zeta", "--json"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    original := "target = 'zeta'\n"
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(original), 0600); err != nil {
        t.Fatal(err)
    }
    c := uxATestFrom(t, o, "1\n3\n1\n2\n5\n", "en")
    before, _ := c.app.Machines.Targets(context.Background(), "")
    refs, err := o.State.Runs(before.Project.ProjectID)
    if err != nil || len(refs) != 1 {
        t.Fatal(refs, err)
    }
    if _, err := c.uxAMachineHub(context.Background()); err != nil {
        t.Fatal(err, c.options.Err)
    }
    after, err := c.app.Machines.Targets(context.Background(), "")
    later, readErr := o.State.Runs(before.Project.ProjectID)
    if err != nil || readErr != nil || after.Project.Target != "lab" || after.Project.Available || !reflect.DeepEqual(refs, later) {
        t.Fatal(after, err, later, readErr)
    }
    if data, err := os.ReadFile(filepath.Join(o.CWD, "loaf.toml")); err != nil || string(data) != original {
        t.Fatal(string(data), err)
    }
    if !strings.Contains(c.options.Err.(*bytes.Buffer).String(), c.output.locale.Text("ux_a_choose_project")) {
        t.Fatal("selected alias removal offered no re-selection")
    }
}

func TestUXACloudDetailsAreFrozenPublicIdentityOnly(t *testing.T) {
    c := uxATestCall(t, "1\n", "en")
    entry := client.TargetEntry{Name: "cloud-lab", Target: config.Target{RemoteBase: "~/projects", Cloud: &cloud.Binding{
        Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "frozen-account", ID: "instance-42"},
        Settings: cloud.Account{Credential: cloud.CredentialRef{Key: "private-secret-key", Env: "PRIVATE_TOKEN"}},
    }}}
    c.app.Machines.Settings.Targets[entry.Name] = entry.Target
    picked, err := c.uxAPickTarget(context.Background(), "")
    if err != nil || picked != entry.Name {
        t.Fatal(picked, err)
    }
    text := c.uxATargetDocument(entry).Content(70, false, true)
    for _, value := range []string{"frozen-account", "pro", "instance-42", "~/projects"} {
        if !strings.Contains(text, value) {
            t.Fatal(value, text)
        }
    }
    for _, value := range []string{"private-secret-key", "PRIVATE_TOKEN", "account_settings"} {
        if strings.Contains(text, value) || strings.Contains(c.options.Err.(*bytes.Buffer).String(), value) {
            t.Fatal("private binding value displayed", text)
        }
    }
    if found := uxAFilterTargets([]client.TargetEntry{entry}, "INSTANCE-42"); len(found) != 1 {
        t.Fatal(found)
    }
}

func TestUXALongNamesAndUnicodeRenderingStayDistinct(t *testing.T) {
    long := strings.Repeat("long", 16)
    c := uxATestCall(t, "1\n", "zh-Hans", long)
    picked, err := c.uxAPickTarget(context.Background(), "")
    if err != nil || picked != long {
        t.Fatal(picked, err)
    }
    if !strings.Contains(c.options.Err.(*bytes.Buffer).String(), long) {
        t.Fatal("long aliases must remain distinguishable in the selector")
    }
    entry := client.TargetEntry{Name: "中文显示测试", Target: config.Target{Destination: "host", RemoteBase: "~/项目"}}
    if len(uxAFilterTargets([]client.TargetEntry{entry}, "中文")) != 1 || !strings.Contains(c.uxATargetDocument(entry).Content(70, false, true), "中文显示测试") {
        t.Fatal("Unicode display/search lost identity")
    }
    if config.ValidName(entry.Name) {
        t.Fatal("update the explicit ASCII input contract when shared config changes")
    }
}

func TestUXAPickerInitialOnLaterPageAndRegistrationCancel(t *testing.T) {
    c := uxATestCall(t, "\n", "en", "a", "b", "c", "d", "e", "f", "g", "h")
    picked, err := c.uxAPickTarget(context.Background(), "h")
    if err != nil || picked != "h" || !strings.Contains(c.options.Err.(*bytes.Buffer).String(), "Page 2 of 2") {
        t.Fatal(picked, err, c.options.Err)
    }
    c = uxATestCall(t, "lab\nlab-host\n4\n", "en")
    if _, err := c.uxAMachineAdd(context.Background()); !errors.Is(err, interaction.ErrCanceled) {
        t.Fatal(err)
    }
    for _, path := range []string{c.app.Machines.Config.ConfigFile, c.app.Machines.State.Path} {
        if _, err := os.Stat(path); !os.IsNotExist(err) {
            t.Fatal("canceled registration wrote state", path, err)
        }
    }
}

type uxANoInput struct{ t *testing.T }

func (r uxANoInput) Read([]byte) (int, error) {
    r.t.Error("direct or batch command attempted a prompt")
    return 0, io.EOF
}

func TestUXADirectAndBatchPathsDoNotPrompt(t *testing.T) {
    for _, batch := range []bool{false, true} {
        c := uxATestCall(t, "", "en")
        c.policy.Batch = batch
        c.options.In = uxANoInput{t}
        c.input = interaction.New(interaction.Options{Interactive: !batch, In: c.options.In, Err: c.options.Err})
        c.grammar.Machine.Add.Name, c.grammar.Machine.Add.SSH = "lab", "lab-host"
        c.grammar.Machine.Add.RemoteDir = "~/projects"
        c.grammar.Machine.Use.Name, c.grammar.Machine.Show.Name, c.grammar.Machine.Remove.Name = "lab", "lab", "lab"
        for _, path := range []string{"machine add", "machine use", "machine list", "machine show", "machine remove"} {
            if _, err := c.uxAMachineCommand(context.Background(), path); err != nil {
                t.Fatal(batch, path, err)
            }
        }
        if c.options.Err.(*bytes.Buffer).Len() != 0 {
            t.Fatal("direct command added UI", c.options.Err)
        }
    }
    for _, path := range []string{"machine add", "machine use", "machine show", "machine remove"} {
        c := uxATestCall(t, "", "en")
        c.policy.Batch = true
        result, err := c.uxAMachineCommand(context.Background(), path)
        var f *fault.Error
        if result != nil || !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
            t.Fatal(path, result, err)
        }
    }
    for _, args := range [][]string{{"machine", "--json"}, {"machine", "list", "--json"}, {"machine", "add", "--json"}, {"machine", "use", "--json"}, {"machine", "show", "--json"}, {"machine", "remove", "--json"}} {
        o := fixture(t)
        o.Interactive, o.In = true, uxANoInput{t}
        _, out, stderr := run(t, o, args...)
        var value envelope
        if err := json.Unmarshal([]byte(out), &value); err != nil || stderr != "" {
            t.Fatal(args, out, stderr, err)
        }
    }
}

func TestUXAUseOnlyChangesTheCurrentProject(t *testing.T) {
    c := uxATestCall(t, "2\n", "en", "alpha", "beta")
    if err := os.WriteFile(filepath.Join(c.app.Machines.CWD, "loaf.toml"), []byte("target = 'alpha'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    result, err := c.uxAMachineCommand(context.Background(), "machine use")
    if err != nil || result.(client.UseResult).Target != "beta" {
        t.Fatal(result, err)
    }
    first, err := c.app.Machines.Targets(context.Background(), "")
    if err != nil || first.Project.Target != "beta" || first.Project.Source != "personal" {
        t.Fatal(first, err)
    }
    c.app.Machines.CWD = t.TempDir()
    second, err := c.app.Machines.Targets(context.Background(), "")
    if err != nil || second.Project.Target != "" || second.Project.ProjectID != "" {
        t.Fatal("project choice leaked into another directory", second, err)
    }
}
