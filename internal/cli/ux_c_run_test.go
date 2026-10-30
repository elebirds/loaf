package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

func uxCCall(t *testing.T, o testOptions, language, input string, args ...string) *invocation {
    t.Helper()
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l, err := i18n.New(language, nil)
    if err != nil {
        t.Fatal(err)
    }
    g := &grammar{}
    parser, err := newParser(g)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := parser.Parse(args); err != nil {
        t.Fatal(err)
    }
    o.In = strings.NewReader(input)
    o.Out, o.Err = &bytes.Buffer{}, &bytes.Buffer{}
    o.Interactive = true
    return &invocation{ui: true,
        app: client.New(o.Dependencies, settings), options: o.Options, grammar: g,
        output: &presenter{locale: l, out: o.Out, err: o.Err, json: g.JSON, command: args[0]},
        input:  interaction.New(interaction.Options{Interactive: true, In: o.In, Err: o.Err}),
        policy: client.CallPolicy{Batch: g.JSON},
    }
}

type uxCChecker struct {
    calls  int
    err    error
    native func() error
}

func (f *uxCChecker) Check(context.Context, config.Target, string, bool) (ssh.Report, error) {
    f.calls++
    if f.native != nil {
        if err := f.native(); err != nil {
            return ssh.Report{}, err
        }
    }
    return ssh.Report{Arch: "aarch64", RemoteDir: "/remote/project"}, f.err
}

type uxCSender struct {
    calls int
    err   error
    input ssh.SyncInput
    after func()
}

func (f *uxCSender) Sync(_ context.Context, _ config.Target, _, _ string, in ssh.SyncInput, _ bool) (ssh.Transfer, error) {
    f.calls++
    f.input = in
    if f.after != nil {
        f.after()
    }
    return ssh.Transfer{}, f.err
}

type uxCRemote struct {
    runRemote
    launches, queries, follows int
    launchErr, observeErr      error
}

func (f *uxCRemote) Control(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (*wire.RunRecord, error) {
    if req.Action == "launch" {
        f.launches++
    } else {
        f.queries++
    }
    if f.launchErr != nil {
        f.req = req
        return nil, f.launchErr
    }
    return f.runRemote.Control(ctx, target, runner, req, batch)
}
func (f *uxCRemote) Observe(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (wire.Observation, error) {
    f.queries++
    if f.observeErr != nil {
        return wire.Observation{}, f.observeErr
    }
    return f.runRemote.Observe(ctx, target, runner, req, batch)
}
func (f *uxCRemote) Follow(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool, emit func(wire.LogEvent) error) error {
    f.follows++
    if f.observeErr != nil {
        return f.observeErr
    }
    return f.runRemote.Follow(ctx, target, runner, req, batch, emit)
}

func uxCFixture(t *testing.T, project string) (testOptions, *uxCChecker, *uxCSender, *uxCRemote) {
    t.Helper()
    o := fixture(t)
    checker, sender, remote := &uxCChecker{}, &uxCSender{}, &uxCRemote{}
    o.Runs = &execution.Service{Index: o.State, Check: checker, Runner: func(context.Context, config.Target, string, bool) (string, error) { return "/remote/runner", nil }, Send: sender, Remote: remote}
    if err := o.Config.Add(context.Background(), "lab", config.Target{Destination: "fixture", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    if project != "" {
        if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(project), 0600); err != nil {
            t.Fatal(err)
        }
    }
    return o, checker, sender, remote
}

func TestUXCRunCompleteCallsAndBatchNeverPrompt(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{{"run"}, {"run", "--json"}, {"run", "--wait", "0s", "--", "echo", "literal $HOME", ";", "--"}, {"run", "--dry-run"}} {
            o, check, _, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='printf done'\nwait='1s'\n")
            c := uxCCall(t, o, language, "", args...)
            data, err := c.uxCRunCommand(context.Background())
            if err != nil || data == nil {
                t.Fatal(args, data, err)
            }
            if c.grammar.Run.DryRun {
                if check.calls != 0 || remote.launches != 0 {
                    t.Fatal("preview connected")
                }
            } else if remote.launches != 1 {
                t.Fatal("complete call not submitted once", remote.launches)
            }
            if c.grammar.Run.Wait != nil && remote.follows != 0 {
                t.Fatal("explicit zero ignored")
            }
            if c.output.json {
                if code := c.output.finish(data, err); code != 0 {
                    t.Fatal(code)
                }
                decoder := json.NewDecoder(strings.NewReader(uxCString(c.output.out)))
                var result map[string]any
                if err := decoder.Decode(&result); err != nil {
                    t.Fatal(err)
                }
                if err := decoder.Decode(&result); !errors.Is(err, io.EOF) {
                    t.Fatal("multiple JSON results", err)
                }
                if uxCString(c.output.err) != "" {
                    t.Fatal("JSON progress leaked", uxCString(c.output.err))
                }
            }
        }
    }
    for _, args := range [][]string{{"run", "--json"}, {"run", "--dry-run"}} {
        o, checker, _, remote := uxCFixture(t, "")
        c := uxCCall(t, o, "en", "", args...)
        if _, err := c.uxCRunCommand(context.Background()); err == nil || errors.Is(err, io.EOF) {
            t.Fatal("invalid direct call prompted or succeeded", err)
        }
        if checker.calls != 0 || remote.launches != 0 {
            t.Fatal("missing input connected")
        }
    }
    o, _, _, _ := uxCFixture(t, "")
    c := uxCCall(t, o, "en", "", "run")
    c.policy.Batch = true
    if _, err := c.uxCRunCommand(context.Background()); err == nil || errors.Is(err, io.EOF) {
        t.Fatal(err)
    }
}

func uxCString(w io.Writer) string { return w.(*bytes.Buffer).String() }

func TestUXCRunFillsOnlyMissingInputsAndKeepsDraftOnBack(t *testing.T) {
    for _, entry := range []struct {
        name, project, input string
        want                 []string
    }{
        {"command", "target='lab'", "2\npython3 'a b.py' '$HOME' ';' --\n1\n", []string{"python3", "a b.py", "$HOME", ";", "--"}},
        {"machine", "[run]\ncommand='printf done'", "1\n1\n", []string{"/bin/sh", "-c", "printf done"}},
        {"both", "", "1\n2\necho ready\n1\n", []string{"echo", "ready"}},
    } {
        t.Run(entry.name, func(t *testing.T) {
            o, _, _, remote := uxCFixture(t, entry.project)
            c := uxCCall(t, o, "en", entry.input, "run")
            data, err := c.uxCRunCommand(context.Background())
            if err != nil || data == nil || remote.launches != 1 || !reflect.DeepEqual(remote.req.Args, entry.want) {
                t.Fatal(data, err, remote, uxCString(c.output.err))
            }
            project, err := o.State.Existing(o.CWD)
            if err != nil || project.Target != "" {
                t.Fatal("temporary choice persisted", project, err)
            }
        })
    }
    o, _, _, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo old'")
    c := uxCCall(t, o, "en", "3\n1\n2\necho 'new value'\n3\n1\n5\n1\n", "run")
    in, _ := c.uxCRunArgs()
    if _, err := c.uxCRunDraft(context.Background(), in); err != nil || !reflect.DeepEqual(remote.req.Args, []string{"echo", "new value"}) || remote.launches != 1 {
        t.Fatal(remote.req, err, uxCString(c.output.err))
    }
}

func TestUXCRunReviewBackIsOfflineAndRedacted(t *testing.T) {
    project := "target='lab'\n[run]\ncommand='printf ready'\nsetup='true'\nwait='30s'\n[run.env]\nTOKEN='private-never-print'\n[outputs]\npaths=['results']\n"
    o, checker, sender, remote := uxCFixture(t, project)
    c := uxCCall(t, o, "zh-Hans", "4\n", "run", "--wait", "0s")
    data, err := c.uxCRun(context.Background())
    if err != nil || data != nil || checker.calls != 0 || sender.calls != 0 || remote.launches != 0 {
        t.Fatal(data, err)
    }
    text := uxCString(c.output.err)
    if strings.Contains(text, "private-never-print") || !strings.Contains(text, "0s") || !strings.Contains(text, "results") || !strings.Contains(text, "/bin/sh -c") {
        t.Fatal(text)
    }
    if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
        t.Fatal("review created state", err)
    }
    after, _ := os.ReadFile(filepath.Join(o.CWD, "loaf.toml"))
    if string(after) != project {
        t.Fatal("review wrote project config")
    }
}

func TestUXCRunPreviewAndActualRequestAgree(t *testing.T) {
    project := "target='lab'\n[run]\ncommand='printf done'\nsetup='true'\nwait='3s'\n[run.env]\nPRIVATE='secret-value'\n[outputs]\npaths=['results/', 'results', 'metrics']\n[sync]\nrespect_gitignore=false\nexclude=['scratch/']\n"
    for _, args := range [][]string{nil, {"printf", "", "$HOME", ";", "中 文"}} {
        o, _, sender, remote := uxCFixture(t, project)
        c := uxCCall(t, o, "en", "", "run")
        in := client.RunInput{Args: args, Wait: new("0s"), DryRun: true}
        preview, err := c.app.Projects.Run(context.Background(), in, client.RunFeedback{})
        if err != nil {
            t.Fatal(err)
        }
        if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
            t.Fatal("preview created state")
        }
        in.DryRun = false
        data, err := c.uxCRunInput(context.Background(), in)
        if err != nil {
            t.Fatal(err)
        }
        view := data.(execution.View)
        if !reflect.DeepEqual(preview.Preview.Args, remote.req.Args) || !reflect.DeepEqual(preview.Preview.Outputs, view.Ref.Outputs) || remote.req.Env["PRIVATE"] != "secret-value" || remote.req.Setup != "true" || len(sender.input.Paths) != len(preview.Preview.Source.Files) {
            t.Fatal(preview.Preview, view, remote.req)
        }
        if remote.follows != 0 || preview.Preview.WaitMillis != 0 || preview.Preview.Source.Policy.RespectGitignore {
            t.Fatal("explicit settings lost")
        }
    }
}

func TestUXCRunFailuresKeepStageIdentityAndNeverResubmit(t *testing.T) {
    for _, stage := range []string{"source", "auth", "sync", "prepare", "submit", "observe", "cancel-observe"} {
        t.Run(stage, func(t *testing.T) {
            o, checker, sender, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo ready'")
            wait := "0s"
            switch stage {
            case "source":
                if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("target='lab'\n[run]\ncommand='echo ready'\n[sync]\nexclude=['!unsafe']"), 0600); err != nil {
                    t.Fatal(err)
                }
            case "auth":
                checker.err = fault.At(fault.New("SSH_AUTH_FAILED", "ssh_failed", nil), "preflight")
            case "sync":
                sender.err = fault.At(fault.New("SYNC_FAILED", "sync_failed", nil), "sync")
            case "prepare":
                remote.launchErr = &ssh.RemoteError{Code: "COPY_FAILED"}
            case "submit":
                remote.launchErr = fault.New("SSH_FAILED", "ssh_failed", nil)
            case "observe":
                wait = "1s"
                remote.observeErr = fault.New("SSH_FAILED", "ssh_failed", nil)
            case "cancel-observe":
                wait = "1s"
                remote.observeErr = context.Canceled
            }
            c := uxCCall(t, o, "en", "", "run", "--wait", wait)
            data, err := c.uxCRunCommand(context.Background())
            if err == nil {
                t.Fatal("failure was lost", data)
            }
            if stage == "source" || stage == "auth" {
                if data != nil || sender.calls != 0 || remote.launches != 0 {
                    t.Fatal(data, sender.calls, remote.launches)
                }
                if stage == "source" && checker.calls != 0 {
                    t.Fatal("invalid source connected")
                }
                return
            }
            view, ok := data.(execution.View)
            if !ok || view.ID == "" || view.Target != "lab" || view.Ref.ID != view.ID {
                t.Fatal(data, err)
            }
            want := "preparing"
            if stage == "submit" {
                want = "uncertain"
            }
            if stage == "observe" || stage == "cancel-observe" {
                want = "submitted"
            }
            if view.Submission != want || remote.launches > 1 || sender.calls != 1 {
                t.Fatal(view, remote, err)
            }
            saved, saveErr := o.State.Run(view.Ref.ProjectID, view.ID)
            if saveErr != nil || saved.Submission != want || saved.Target != "lab" {
                t.Fatal(saved, saveErr)
            }
        })
    }
}

func TestUXCRunWaitConsumesLogOnce(t *testing.T) {
    o, _, _, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo ready'")
    c := uxCCall(t, o, "en", "", "run", "--wait", "1s")
    data, err := c.uxCRunCommand(context.Background())
    if err != nil || remote.follows != 1 {
        t.Fatal(data, err)
    }
    if code := c.output.finish(data, err); code != 0 {
        t.Fatal(code)
    }
    if strings.Count(uxCString(c.output.out), "中文 log") != 1 {
        t.Fatal(uxCString(c.output.out))
    }
}

func TestUXCExecutePlainMenuAndCanonicalArgv(t *testing.T) {
    for _, env := range []map[string]string{{"LANG": "C", "TERM": "dumb"}, {"LANG": "C", "TERM": "xterm-256color", "NO_COLOR": "1"}} {
        o, _, _, remote := uxCFixture(t, "target='lab'")
        o.Interactive, o.TerminalError, o.Env = true, true, env
        o.In = strings.NewReader("2\npython3 'a b.py' '$HOME' ';' --\n1\n")
        code, out, stderr := run(t, o, "-i", "run", "--wait", "0s")
        if code != 0 || !strings.Contains(out, "r_") || remote.launches != 1 || strings.Contains(stderr, "\x1b[") || !reflect.DeepEqual(remote.req.Args, []string{"python3", "a b.py", "$HOME", ";", "--"}) {
            t.Fatal(code, out, stderr, remote.req)
        }
    }
    o, checker, _, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo ready'")
    c := uxCCall(t, o, "en", "4\n", "run")
    if _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "run"}); err != nil || checker.calls != 0 || remote.launches != 0 {
        t.Fatal("root run route not connected", err)
    }
}

func TestUXCRunRootCancellationRetainsSavedIdentity(t *testing.T) {
    o, _, sender, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo ready'")
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sender.after, sender.err = cancel, context.Canceled
    c := uxCCall(t, o, "en", "1\n", "run")
    data, err := c.uxCRun(ctx)
    if data != nil || !errors.Is(err, context.Canceled) || remote.launches != 0 {
        t.Fatal(data, err, remote.launches)
    }
    saved, err := o.State.Run(sender.input.ProjectID, sender.input.RunID)
    if err != nil || saved.Submission != "preparing" || saved.Target != "lab" || !strings.Contains(uxCString(c.output.err), saved.ID) {
        t.Fatal("canceled menu lost its saved identity", saved, err, uxCString(c.output.err))
    }
    history, err := o.State.Runs(sender.input.ProjectID)
    if err != nil || len(history) != 0 {
        t.Fatal("canceled preparation entered submitted history", history, err)
    }
}

func TestUXCRunOutcomeNavigatesFrozenIdentity(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, submission := range []string{"preparing", "uncertain", "submitted"} {
            t.Run(language+"/"+submission, func(t *testing.T) {
                ctx := context.Background()
                o, _, sender, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo ready'")
                if submission == "preparing" {
                    sender.err = fault.New("SYNC_FAILED", "sync_failed", nil)
                } else if submission == "uncertain" {
                    remote.launchErr = fault.New("SSH_FAILED", "ssh_failed", nil)
                }
                original := uxCCall(t, o, language, "", "run", "--wait", "0s")
                data, cause := original.uxCRunCommand(ctx)
                result, ok := data.(execution.View)
                if !ok || result.Submission != submission || result.ID == "" {
                    t.Fatal(data, cause)
                }

                // A newer submission and a different current machine must not
                // redirect the result page's explicitly selected task or logs.
                sender.err, remote.launchErr = nil, nil
                data, err := original.uxCRunCommand(ctx)
                if err != nil {
                    t.Fatal(err)
                }
                newer := data.(execution.View)
                if newer.ID == result.ID {
                    t.Fatal("fixture did not create a distinct latest run")
                }
                if err := o.Config.Add(ctx, "other", config.Target{Destination: "other-fixture", RemoteBase: "~/projects"}, false); err != nil {
                    t.Fatal(err)
                }
                if _, err := o.State.Select(ctx, o.CWD, "other"); err != nil {
                    t.Fatal(err)
                }
                launches, queries, follows := remote.launches, remote.queries, remote.follows
                for _, destination := range []string{"task", "logs"} {
                    if destination == "logs" && submission == "preparing" {
                        continue
                    }
                    c := uxCCall(t, o, language, "", "run", "--target", "other")
                    if !c.uxRouteAvailable(destination) {
                        t.Fatalf("result navigation is not integrated: %s", destination)
                    }
                    outcomeBack := len(c.uxCRunOutcomeChoices(result))
                    choice := uxDChoiceIndex(t, c.uxCRunOutcomeChoices(result), destination)
                    input, title := fmt.Sprintf("%d\n4\n%d\n", choice, outcomeBack), c.output.locale.Text("ux_e_title")
                    if destination == "task" {
                        saved, err := c.app.Projects.Inspect(ctx, result.ID)
                        if err != nil {
                            t.Fatal(err)
                        }
                        back := len(c.uxDTaskChoices(uxDTaskState{saved: saved}))
                        input, title = fmt.Sprintf("%d\n%d\n%d\n", choice, back, outcomeBack), c.output.locale.Text("ux_d_task")
                    }
                    c.input = interaction.New(interaction.Options{Interactive: true, In: strings.NewReader(input), Err: c.output.err})
                    if err := c.uxCRunOutcome(ctx, result, cause); err != nil {
                        t.Fatal(destination, err, uxCString(c.output.err))
                    }
                    rendered := uxCString(c.output.err)
                    start := strings.LastIndex(rendered, "> "+title+"\n")
                    if start < 0 || !strings.Contains(rendered[start:], result.ID) || !strings.Contains(rendered[start:], "lab") || strings.Contains(rendered[start:], newer.ID) || strings.Contains(rendered[start:], "other") {
                        t.Fatal("navigation changed the saved identity", destination, rendered)
                    }
                    if submission == "preparing" && strings.Contains(rendered[:start], c.output.locale.Text("ux_c_open_logs")) {
                        t.Fatal("preparing outcome offered logs before acceptance", rendered[:start])
                    }
                    if remote.launches != launches || remote.queries != queries || remote.follows != follows || *c.grammar.Target != "other" {
                        t.Fatal("opening navigation executed or mutated its parent", destination, remote)
                    }
                }
            })
        }
    }
}
