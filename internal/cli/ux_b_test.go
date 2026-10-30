package cli

import (
    "bytes"
    "context"
    "errors"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
)

func uxBCall(t *testing.T, o testOptions, language, input string) *invocation {
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
    if _, err = parser.Parse([]string{"check"}); err != nil {
        t.Fatal(err)
    }
    o.In, o.Interactive = strings.NewReader(input), true
    return &invocation{ui: true,
        app: client.New(o.Dependencies, settings), options: o.Options, grammar: model,
        output: &presenter{locale: l, out: o.Out, err: o.Err},
        input:  interaction.New(interaction.Options{In: o.In, Err: o.Err, Interactive: true, PlainMultiHelp: l.Text("ux_core_multi_plain")}),
    }
}

func TestUXBCommandSharesLiteralRulesAndExplicitShell(t *testing.T) {
    want := []string{"printf", "%s", "space 中文", "$HOME", "$(touch nope)", "*", ";", "", "--"}
    args, err := uxBExecArgs("argv", `printf '%s' 'space 中文' '$HOME' '$(touch nope)' '*' ';' '' --`)
    if err != nil || !reflect.DeepEqual(args, want) {
        t.Fatal(args, err)
    }
    for _, text := range []string{"", `'' x`, "echo $HOME", "echo *", "echo x; touch nope", "echo x | cat", "echo $(pwd)", "A=x echo x"} {
        if _, err := uxBExecArgs("argv", text); err == nil {
            t.Fatal("accepted expansion/execution syntax", text)
        }
    }
    script := `printf '%s' "$HOME"; echo x | cat`
    args, err = uxBExecArgs("shell", script)
    if err != nil || !reflect.DeepEqual(args, []string{"/bin/sh", "-c", script}) {
        t.Fatal(args, err)
    }
    for _, mode := range []string{"shell", "argv", "other"} {
        if _, err := uxBExecArgs(mode, ""); err == nil {
            t.Fatal(mode)
        }
    }
}

func TestUXBExecMenuCallsOnceAndPreservesNativeStreams(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, status := range []string{"succeeded", "failed", "unconfirmed"} {
            o, operation := operationFixture(t)
            original := error(nil)
            if status == "unconfirmed" {
                original = fault.New("EXEC_UNCONFIRMED", "exec_unconfirmed", nil)
            } else if status == "failed" {
                original = fault.New("EXEC_FAILED", "exec_failed", map[string]any{"Value": 7})
            }
            operation.result.Status, operation.err = status, original
            operation.output = []byte("raw \x1b[31mbytes\x1b[0m\n")
            c := uxBCall(t, o, language, "1\nprintf '%s' 'space 中文' '$HOME' '' --\n1\n")
            name := "demo"
            data, err := c.uxBConnection(context.Background(), "exec", &name)
            if data != nil || !errors.Is(err, original) || operation.calls != 1 || operation.input.Batch {
                t.Fatal(status, data, err, operation.calls)
            }
            if !reflect.DeepEqual(operation.input.Args, []string{"printf", "%s", "space 中文", "$HOME", "", "--"}) || o.Out.(*bytes.Buffer).String() != string(operation.output) {
                t.Fatal(operation.input.Args, o.Out)
            }
            if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
                t.Fatal("exec created project state", err)
            }
            if strings.Contains(o.Err.(*bytes.Buffer).String(), "<no value>") {
                t.Fatal(o.Err)
            }
        }
    }
}

func TestUXBForwardReviewAndBackRetainDraft(t *testing.T) {
    for _, tc := range []struct {
        input        string
        calls, local int
    }{
        {"6006\n\n1\n", 1, 6006},
        {"6006\n16006\n1\n", 1, 16006},
        {"6006\n16006\n2\n\n26006\n1\n", 1, 26006},
        {"6006\n16006\n3\n", 0, 0},
        {"0\n65536\n6006\n-1\n65536\n16006\n1\n", 1, 16006},
    } {
        o, operation := operationFixture(t)
        c := uxBCall(t, o, "en", tc.input)
        name := "demo"
        _, err := c.uxBConnection(context.Background(), "forward", &name)
        if tc.calls == 0 {
            if !errors.Is(err, interaction.ErrCanceled) {
                t.Fatal(err)
            }
        } else if err != nil || operation.remote != 6006 || operation.local != tc.local {
            t.Fatal(err, operation)
        }
        if operation.calls != tc.calls || o.Out.(*bytes.Buffer).Len() != 0 {
            t.Fatal(operation.calls, o.Out)
        }
        output := o.Err.(*bytes.Buffer).String()
        if !strings.Contains(output, "Ctrl+C") || !strings.Contains(output, "127.0.0.1:") {
            t.Fatal(output)
        }
    }
}

func TestUXBSelectorsOnlyChooseRegisteredAliasesAndNeverPersist(t *testing.T) {
    o, operation := operationFixture(t)
    c := uxBCall(t, o, "en", "1\n")
    before, _ := os.ReadFile(o.Config.ConfigFile)
    _, err := c.uxBConnection(context.Background(), "ssh", nil)
    after, _ := os.ReadFile(o.Config.ConfigFile)
    if err != nil || operation.calls != 1 || !operation.login || !bytes.Equal(before, after) {
        t.Fatal(err, operation)
    }
    if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
        t.Fatal("picker persisted a preference", err)
    }
    empty := fixture(t)
    c = uxBCall(t, empty, "en", "\n")
    if _, err := c.uxBConnection(context.Background(), "ssh", nil); !errors.Is(err, interaction.ErrCanceled) {
        t.Fatal(err)
    }
    c = uxBCall(t, empty, "en", "")
    if _, err := c.uxBCheck(context.Background()); err == nil {
        t.Fatal("empty machines should not run checks")
    }
}

type uxBCheckFunc func(context.Context, config.Target, string, bool) (ssh.Report, error)

func (f uxBCheckFunc) Check(ctx context.Context, target config.Target, dir string, batch bool) (ssh.Report, error) {
    return f(ctx, target, dir, batch)
}

func TestUXBCheckMenuBoundedPartialCoverageAndReadOnlyRunner(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o, _ := operationFixture(t)
        if err := o.Config.Add(context.Background(), "failed", config.Target{Destination: "failed", RemoteBase: "~/projects"}, false); err != nil {
            t.Fatal(err)
        }
        calls := 0
        o.Checker = uxBCheckFunc(func(ctx context.Context, target config.Target, _ string, batch bool) (ssh.Report, error) {
            calls++
            deadline, ok := ctx.Deadline()
            if !ok || time.Until(deadline) > 20*time.Second || time.Until(deadline) < 19*time.Second || batch {
                t.Fatal("lost check budget / authentication policy", deadline, batch)
            }
            if target.Destination == "failed" {
                return ssh.Report{}, fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Permission denied secret-endpoint raw-secret"})
            }
            return ssh.Report{OS: "Linux", Arch: "aarch64", Local: map[string]ssh.Tool{}, Remote: map[string]ssh.Tool{}, Resources: &ssh.ResourceObservation{}}, nil
        })
        // Select two rows, then return from their retained batch result.
        c := uxBCall(t, o, language, "1,2\n3\n")
        before, _ := os.ReadFile(o.Config.ConfigFile)
        data, err := c.uxBCheck(context.Background())
        after, _ := os.ReadFile(o.Config.ConfigFile)
        output := o.Err.(*bytes.Buffer).String()
        if err != nil || data != nil || calls != 2 || !bytes.Equal(before, after) || o.Out.(*bytes.Buffer).Len() != 0 || !strings.Contains(output, "2/2") || !strings.Contains(output, "failed") || strings.Contains(output, "raw-secret") {
            t.Fatal(data, err, calls, output)
        }
        project, err := o.State.Existing(o.CWD)
        if err != nil || project.Target != "" {
            t.Fatal("checks changed selection", project, err)
        }
        // No runner verifier was installed: ordinary check must not need one.
    }
}

func TestUXBCheckDocumentsUnknownsAndLongTechnicalPaths(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(language, nil)
        path := "/temporary/" + strings.Repeat("long-tool-directory/", 12) + "python"
        row := checkResult{Target: "fixture", Steps: map[string]string{"connect": "verified", "authentication": "verified", "platform": "verified", "runner": "not_checked"}, Report: ssh.Report{OS: "Linux", Arch: "aarch64", Remote: map[string]ssh.Tool{"python": {Path: path, Version: "version\nsecond line\x1b[31m"}}}}
        main := uxBCheckDocument(row, l, false).Content(68, false, false)
        detail := uxBCheckDocument(row, l, true).Body
        if strings.Contains(main, path) || strings.Contains(main, "version") || !strings.Contains(main, l.Text("probe_unknown")) || !strings.Contains(detail, path) || strings.Contains(detail, "\x1b") {
            t.Fatal(main, detail)
        }
        row.Error = &errorView{Code: "SSH_FAILED", FailureKind: "authentication", Detail: "private-endpoint secret-command secret-env"}
        main = uxBCheckDocument(row, l, false).Body
        if strings.Contains(main, "secret") || strings.Contains(main, "private-endpoint") || !strings.Contains(main, l.Text("ux_b_recover_authentication")) {
            t.Fatal(main)
        }
        zero := int64(0)
        row.Report.Resources = &ssh.ResourceObservation{GPUs: []ssh.GPUObservation{{Index: 0, Model: "fixture GPU", MemoryUsedBytes: &zero}}}
        main = uxBCheckDocument(row, l, false).Body
        if !strings.Contains(main, "0.00/") || !strings.Contains(main, l.Text("probe_unknown")) {
            t.Fatal(main)
        }
    }
}

func TestUXBRecoveryKeepsTrustDecisionsDistinct(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(language, nil)
        for _, kind := range []string{"authentication", "configuration", "name_resolution", "connection_refused", "connection_timeout", "connection_reset", "connection_closed", "observation_timeout", "canceled", "host_trust", "remote_prerequisite", "runner", "operation_failed", "cloud_api"} {
            view := &errorView{Code: "SSH_FAILED", FailureKind: kind}
            if uxBFailureSummary(view, l) == "" || uxBRecovery(view, l) == "" {
                t.Fatal(kind)
            }
        }
        unknown := &errorView{FailureKind: "host_trust", FailureReason: "host_key_unknown"}
        changed := &errorView{FailureKind: "host_trust", FailureReason: "host_key_changed"}
        if uxBFailureSummary(unknown, l) == uxBFailureSummary(changed, l) || uxBRecovery(unknown, l) == uxBRecovery(changed, l) || strings.Contains(uxBRecovery(changed, l), "ssh-keygen -R") {
            t.Fatal("lost trust distinction")
        }
        lost := &errorView{Code: "EXEC_UNCONFIRMED", FailureKind: "connection_closed"}
        if !strings.Contains(uxBRecovery(lost, l), l.Text("exec_unconfirmed_hint")) {
            t.Fatal("lost no-replay recovery")
        }
    }
}

func TestUXBMenuBatchGuardsAndDirectCallsNeverPrompt(t *testing.T) {
    for _, mode := range []string{"batch", "json"} {
        o, operation := operationFixture(t)
        c := uxBCall(t, o, "en", "")
        c.options.In = uxBNoInput{t}
        c.input = interaction.New(interaction.Options{In: c.options.In, Err: o.Err})
        c.policy.Batch, c.output.json = mode == "batch", mode == "json"
        name := "demo"
        for _, kind := range []string{"ssh", "exec", "forward"} {
            if _, err := c.uxBConnection(context.Background(), kind, &name); !errors.Is(err, interaction.ErrNonInteractive) {
                t.Fatal(kind, err)
            }
        }
        if _, err := c.uxBCheck(context.Background()); !errors.Is(err, interaction.ErrNonInteractive) || operation.calls != 0 {
            t.Fatal(err, operation.calls)
        }
        c.grammar.Target = &name
        c.policy.Batch = true
        data, err := c.uxBDirectCheck(context.Background())
        if err != nil || data.(checkResult).Target != name {
            t.Fatal(data, err)
        }
    }
}

type uxBNoInput struct{ t *testing.T }

func (r uxBNoInput) Read([]byte) (int, error) {
    r.t.Error("unexpected stdin read")
    return 0, io.EOF
}

func TestUXBCheckReportAllowlistIncludesCanceledCoverage(t *testing.T) {
    o, _ := operationFixture(t)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    o.Checker = uxBCheckFunc(func(_ context.Context, _ config.Target, _ string, _ bool) (ssh.Report, error) {
        cancel()
        return ssh.Report{}, fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "private-endpoint secret-command secret-env"})
    })
    c := uxBCall(t, o, "en", "")
    report := filepath.Join(t.TempDir(), "report.json")
    c.grammar.Check.Report = &report
    c.grammar.Check.Targets.Values = []string{"demo"}
    _, err := c.uxBCheck(ctx)
    var problem *fault.Error
    if !errors.As(err, &problem) || problem.Code != "CHECK_PARTIAL" {
        t.Fatal(err)
    }
    data, err := os.ReadFile(report)
    if err != nil || !strings.Contains(string(data), `"failure_kind": "canceled"`) || !strings.Contains(o.Err.(*bytes.Buffer).String(), "1/1") {
        t.Fatal(string(data), err, o.Err)
    }
    for _, secret := range []string{"private-endpoint", "secret-command", "secret-env", "demo"} {
        if strings.Contains(string(data), secret) || strings.Contains(o.Err.(*bytes.Buffer).String(), "private-endpoint") {
            t.Fatal(string(data), o.Err)
        }
    }
}

func TestUXBConfiguredTargetAndExplicitTargetDoNotRedirect(t *testing.T) {
    o, operation := operationFixture(t)
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("target='missing'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    c := uxBCall(t, o, "en", "")
    name := "demo"
    if _, err := c.uxBConnection(context.Background(), "ssh", &name); err != nil || operation.calls != 1 || operation.target.Destination != "gpu" {
        t.Fatal(err, operation)
    }
    name = "missing"
    _, err := c.uxBConnection(context.Background(), "ssh", &name)
    var problem *fault.Error
    if !errors.As(err, &problem) || problem.Code != "BAD_CONFIG" || operation.calls != 1 {
        t.Fatal("unknown target redirected or connected", err, operation)
    }
    if _, err := c.uxBConnection(context.Background(), "unsupported", &name); !errors.As(err, &problem) || operation.calls != 1 {
        t.Fatal("unsupported connection kind was executed", err)
    }
}

func TestUXBPlainMenusHaveNoANSI(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o, operation := operationFixture(t)
        c := uxBCall(t, o, language, "6006\n\n3\n")
        name := "demo"
        if _, err := c.uxBConnection(context.Background(), "forward", &name); !errors.Is(err, interaction.ErrCanceled) || operation.calls != 0 {
            t.Fatal(err, operation.calls)
        }
        if strings.Contains(o.Err.(*bytes.Buffer).String(), "\x1b") || o.Out.(*bytes.Buffer).Len() != 0 {
            t.Fatal("plain menu used ANSI or stdout", o.Err, o.Out)
        }
    }
}

func TestUXBRoutesConsumeConnectionsAndCheckDefaults(t *testing.T) {
    o, operation := operationFixture(t)
    c := uxBCall(t, o, "en", "")
    name := "demo"
    if !c.uxRouteAvailable("ssh") || !c.uxRouteAvailable("check") {
        t.Fatal("00 must consume the B route patch before integrated acceptance")
    }
    if data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "ssh", Target: &name}); err != nil || data != nil || operation.calls != 1 {
        t.Fatal(data, err, operation.calls)
    }
    c = uxBCall(t, o, "en", "\n3\n")
    if data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "check", Target: &name}); err != nil || data != nil {
        t.Fatal(data, err)
    }
    if c.grammar.Target != nil {
        t.Fatal("child route changed parent target")
    }
}

func TestUXBDirectCheckSummaryKeepsTechnicalDataInJSON(t *testing.T) {
    o, _ := operationFixture(t)
    privatePath := "/fixture/" + strings.Repeat("very-long-path/", 10) + "python"
    o.Checker = uxBCheckFunc(func(_ context.Context, _ config.Target, _ string, _ bool) (ssh.Report, error) {
        return ssh.Report{OS: "Linux", Arch: "aarch64", RemoteDir: "/fixture/project", Remote: map[string]ssh.Tool{"python": {Path: privatePath, Version: "Python fixture-version"}}}, nil
    })
    for _, language := range []string{"en", "zh-Hans"} {
        code, out, diagnostic := run(t, o, "check", "--target", "demo", "--lang", language)
        l, _ := i18n.New(language, nil)
        if code != 0 || diagnostic != "" || strings.Contains(out, privatePath) || strings.Contains(out, "fixture-version") || !strings.Contains(out, l.Text("check_step_authentication")) || !strings.Contains(out, l.Text("probe_unknown")) {
            t.Fatal(code, out, diagnostic)
        }
        o.In = uxBNoInput{t}
        code, out, diagnostic = run(t, o, "check", "--target", "demo", "--json", "--lang", language)
        if code != 0 || diagnostic != "" || !strings.Contains(out, privatePath) || !strings.Contains(out, "fixture-version") || strings.Contains(out, "\x1b") {
            t.Fatal(code, out, diagnostic)
        }
    }
}
