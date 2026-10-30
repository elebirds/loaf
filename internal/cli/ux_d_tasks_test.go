package cli

import (
    "bytes"
    "context"
    "errors"
    "os"
    "path/filepath"
    "slices"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/alecthomas/kong"
    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

func uxDCall(t *testing.T, o testOptions, language, input string) *invocation {
    t.Helper()
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l, err := i18n.New(language, nil)
    if err != nil {
        t.Fatal(err)
    }
    o.Out, o.Err = &bytes.Buffer{}, &bytes.Buffer{}
    o.Interactive = true
    o.In = strings.NewReader(input)
    return &invocation{ui: true, app: client.New(o.Dependencies, settings), options: o.Options, grammar: &grammar{},
        output: &presenter{locale: l, out: o.Out, err: o.Err},
        input: interaction.New(interaction.Options{In: o.In, Err: o.Err, Interactive: true, Compact: true,
            ChoiceError: l.Text("ux_core_choice_invalid"), PlainMultiHelp: l.Text("ux_core_multi_plain")}),
    }
}

func uxDChoiceIndex(t *testing.T, choices []interaction.Choice, value string) int {
    t.Helper()
    for i, choice := range choices {
        if choice.Value == value {
            return i + 1
        }
    }
    t.Fatalf("missing choice %q", value)
    return 0
}

func uxDDetailIndices(t *testing.T, c *invocation, id string) (back, stop int) {
    t.Helper()
    saved, err := c.app.Projects.Inspect(context.Background(), id)
    if err != nil {
        t.Fatal(err)
    }
    choices := c.uxDTaskChoices(uxDTaskState{saved: saved})
    return uxDChoiceIndex(t, choices, "back"), uxDChoiceIndex(t, choices, "stop")
}

func TestUXDStatusEntryKeepsExplicitCommands(t *testing.T) {
    for _, args := range [][]string{{"status"}, {"--lang", "zh-Hans", "status"}, {"status", "--wait", "0s"}, {"status", "--local=false"}, {"status", "--local"}, {"status", "--limit", "1"}, {"status", "--name", "name"}, {"status", "--tag", "a"}, {"status", "--target", "retired"}, {"status", "r_id"}, {"status", "--trace"}, {"status", "--json"}} {
        c := uxDCall(t, fixture(t), "en", "")
        parser, err := newParser(c.grammar)
        if err != nil {
            t.Fatal(err)
        }
        traced, err := kong.Trace(parser, args)
        if err != nil {
            t.Fatal(err)
        }
        explicit := uxDStatusOptions(traced)
        if _, err := parser.Parse(args); err != nil {
            t.Fatal(err)
        }
        c.output.json = c.grammar.JSON
        want := slices.Equal(args, []string{"status"}) || slices.Equal(args, []string{"--lang", "zh-Hans", "status"})
        if c.uxDStatusInteractive(explicit) != want {
            t.Fatal(args, explicit, c.grammar.PS)
        }
        c.options.Interactive = false
        if c.uxDStatusInteractive(false) {
            t.Fatal("noninteractive status opened a menu")
        }
    }
}

func TestUXDPublicStatusAndRoutesReadLocalFactsFirst(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o, remote, refs := psFixture(t)
        o.Interactive = true
        o.In = strings.NewReader("6\n")
        code, out, stderr := run(t, o, "-i", "status", "--lang", language)
        l, _ := i18n.New(language, nil)
        if code != 0 || out != "" || remote.calls != 0 || !strings.Contains(stderr, l.Text("ux_d_tasks")) {
            t.Fatal(code, out, stderr, remote.calls)
        }
        for _, args := range [][]string{{"status", "--wait", "0s"}, {"status", "--local=false"}, {"status", "--limit", "1"}, {"status", refs[0].ID}, {"inspect", refs[0].ID}, {"status", "--json"}, {"status", "--local"}} {
            o.In = uxCoreNoInput{t}
            code, out, stderr := run(t, o, args...)
            if code != 0 || out == "" || stderr != "" {
                t.Fatal(args, code, out, stderr)
            }
        }
        calls := remote.calls
        c := uxDCall(t, o, language, "6\n")
        c.grammar.Target = new("replacement")
        if !c.uxRouteAvailable("tasks") || !c.uxRouteAvailable("task") {
            t.Fatal("D entry points have not been integrated by 00")
        }
        if data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "tasks"}); err != nil || data != nil || remote.calls != calls {
            t.Fatal(data, err, remote.calls)
        }
        back, _ := uxDDetailIndices(t, c, refs[0].ID)
        c = uxDCall(t, o, language, strconv.Itoa(back)+"\n")
        c.grammar.Target = new("replacement")
        if data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "task", ID: refs[0].ID}); err != nil || data != nil || remote.calls != calls {
            t.Fatal(data, err, remote.calls)
        }
    }
}

func TestUXDPickerLocalExactIDsAndEmptyProject(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        o, remote, refs := psFixture(t)
        if err := o.State.SaveRunMetadata(context.Background(), refs[0].ProjectID, refs[0].ID, task.RunMetadata{Name: strings.Repeat("中文长名称", 20), Tags: []string{"seed 1", "literal,tag"}}); err != nil {
            t.Fatal(err)
        }
        c := uxDCall(t, o, lang, "1\n")
        c.grammar.Target = new("replacement")
        ids, err := c.uxDPickRuns(context.Background(), false)
        if err != nil || !slices.Equal(ids, []string{refs[0].ID}) || remote.calls != 0 {
            t.Fatal(ids, err, remote.calls)
        }
        c = uxDCall(t, o, lang, "1\n2\n5\n")
        ids, err = c.uxDPickRuns(context.Background(), true)
        if err != nil || !slices.Equal(ids, []string{refs[0].ID, refs[1].ID}) || remote.calls != 0 || c.options.Out.(*bytes.Buffer).Len() != 0 {
            t.Fatal(ids, err, remote.calls)
        }
        empty := fixture(t)
        c = uxDCall(t, empty, lang, "2\n")
        ids, err = c.uxDPickRuns(context.Background(), false)
        if err != nil || len(ids) != 0 || !strings.Contains(c.options.Err.(*bytes.Buffer).String(), c.output.locale.Text("ux_d_empty")) {
            t.Fatal(ids, err)
        }
        if _, err := os.Stat(empty.State.Path); !os.IsNotExist(err) {
            t.Fatal("local picker wrote a state store", err)
        }
    }
}

func TestUXDBrowseRoundTripRefreshAndCancelStop(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        o, remote, refs := psFixture(t)
        c := uxDCall(t, o, lang, "")
        back, stop := uxDDetailIndices(t, c, refs[0].ID)
        c = uxDCall(t, o, lang, "1\n"+strconv.Itoa(back)+"\n5\n4\n1\n"+strconv.Itoa(stop)+"\nn\n"+strconv.Itoa(back)+"\n6\n")
        if _, err := c.uxDTasks(context.Background()); err != nil {
            t.Fatal(err, c.options.Err)
        }
        if remote.calls != 4 || remote.req.Action != "" || c.options.Out.(*bytes.Buffer).Len() != 0 {
            t.Fatal("unexpected calls or stdout", remote.calls, remote.req, c.options.Out)
        }
        out := c.options.Err.(*bytes.Buffer).String()
        for _, id := range []string{"ux_d_tasks", "ux_d_stop_confirm", "ux_d_grace"} {
            if !strings.Contains(out, c.output.locale.Text(id)) {
                t.Fatal(id, out)
            }
        }
    }
}

func TestUXDBrowserFiltersBeforeBoundAndRetainsFailures(t *testing.T) {
    o, remote, refs := psFixture(t)
    o.Runs.Metadata = o.State
    for _, ref := range refs[:2] {
        if err := o.State.SaveRunMetadata(context.Background(), ref.ProjectID, ref.ID, task.RunMetadata{Name: "same 中文", Tags: []string{"a", "b"}}); err != nil {
            t.Fatal(err)
        }
    }
    c := uxDCall(t, o, "en", "")
    b := uxDBrowser{input: client.PSInput{Local: true, Limit: new(1), Name: new("same 中文"), Tags: []string{"a", "b"}}}
    if err := c.uxDLoad(context.Background(), &b); err != nil || remote.calls != 0 || b.history.Eligible != 2 || b.history.Examined != 1 || !b.history.Truncated || b.history.Runs[0].ID != refs[0].ID {
        t.Fatal(b, err, remote.calls)
    }
    b.search = "no match"
    if len(b.rows()) != 0 || b.history.Eligible != 2 || b.history.Limit != 1 {
        t.Fatal("search widened observation", b)
    }
    b.search = "中文"
    b.focus = refs[0].ID
    b.input.Local, b.input.Statuses = false, []string{"succeeded"}
    remote.failID = refs[0].ID
    if err := c.uxDLoad(context.Background(), &b); err != nil || remote.calls != 1 || len(b.rows()) != 1 || b.history.Unresolved != 1 || b.history.StateErrors != 0 || b.focus != refs[0].ID {
        t.Fatal(b, err, remote.calls)
    }
    doc := c.uxDCoverage(b).Content(80, false, false)
    if !strings.Contains(doc, "unexamined 1") || !strings.Contains(doc, "QUERY_PARTIAL") {
        t.Fatal(doc)
    }
    remote.failID = ""
    o.Runs.Observations = unavailableObservationState{}
    if err := c.uxDLoad(context.Background(), &b); err != nil || b.history.Unresolved != 0 || b.history.StateErrors != 1 || b.history.Runs[0].Status != "running" {
        t.Fatal("state failure erased verified facts", b, err)
    }
}

func TestUXDPreparationUncertainAndVersionDetailsStayOffline(t *testing.T) {
    o, remote, refs := psFixture(t)
    for _, submission := range []string{"preparing", "uncertain"} {
        ref := refs[0]
        ref.ID, ref.Submission = "r_ffffffffffffffffffffffff", submission
        if submission == "preparing" {
            ref.AttemptedAt = nil
        }
        if err := o.State.SaveRun(context.Background(), ref); err != nil {
            t.Fatal(err)
        }
        c := uxDCall(t, o, "zh-Hans", "")
        c.app.Projects.Runs = nil
        c.app.Projects.Settings.Targets = nil
        detail, err := c.app.Projects.Inspect(context.Background(), ref.ID)
        if err != nil {
            t.Fatal(err)
        }
        choices := c.uxDTaskChoices(uxDTaskState{saved: detail})
        c.input = interaction.New(interaction.Options{In: strings.NewReader(strconv.Itoa(len(choices)) + "\n"), Err: c.options.Err, Interactive: true})
        if _, err := c.uxDTask(context.Background(), ref.ID); err != nil || remote.calls != 0 {
            t.Fatal(err, remote.calls)
        }
        doc := c.uxDTaskDocument(uxDTaskState{saved: detail}).Content(80, false, false)
        if !strings.Contains(doc, ref.ID) || !strings.Contains(doc, c.uxDSubmission(submission)) || !strings.Contains(doc, "original") {
            t.Fatal(doc)
        }
    }
    c := uxDCall(t, o, "en", "")
    if _, err := c.uxDTask(context.Background(), ""); err == nil {
        t.Fatal("empty task selected latest")
    }
    c.policy.Batch = true
    if _, err := c.uxDPickRuns(context.Background(), false); !errors.Is(err, interaction.ErrNonInteractive) {
        t.Fatal(err)
    }
}

func TestUXDDetailsKeepVerifiedFactBesideQueryAndStateFailures(t *testing.T) {
    o, _, refs := psFixture(t)
    for _, lang := range []string{"en", "zh-Hans"} {
        c := uxDCall(t, o, lang, "")
        saved, err := c.app.Projects.Inspect(context.Background(), refs[0].ID)
        if err != nil {
            t.Fatal(err)
        }
        s := uxDTaskState{saved: saved}
        v := saved.View
        v.Status, v.Phase, v.ObservedAt, v.StateError = "failed", "command", time.Now().UTC(), "STATE_FAILED"
        v.ExitCode = new(7)
        s.accept(v, fault.New("STATE_FAILED", "run_state", nil))
        later := saved.View
        later.QueryError = "SSH_FAILED"
        s.accept(later, fault.New("SSH_FAILED", "ssh_failed", nil))
        doc := c.uxDTaskDocument(s).Content(80, false, false)
        if s.view().Status != "failed" || s.view().ExitCode == nil || *s.view().ExitCode != 7 || !strings.Contains(doc, "SSH_FAILED") || !strings.Contains(doc, "STATE_FAILED") || !strings.Contains(doc, v.ObservedAt.Format(time.RFC3339Nano)) {
            t.Fatal(s, doc)
        }
        for _, kind := range []string{"logs", "outputs", "clean"} {
            route := uxDTaskRoute(kind, saved.ID)
            if err := route.validate(); err != nil || route.Target != nil || kind != "clean" && route.ID != saved.ID || kind == "clean" && !slices.Equal(route.IDs, []string{saved.ID}) {
                t.Fatal(route, err)
            }
        }
    }
}

func TestUXDFiltersCancelAndLiteralTags(t *testing.T) {
    o := fixture(t)
    c := uxDCall(t, o, "en", "1\nnew name\n10\n")
    original := client.PSInput{Local: true, Name: new("old name"), Tags: []string{"seed 1", "a,b"}, Limit: new(3)}
    next, applied, err := c.uxDFilters(context.Background(), original)
    if err != nil || applied || deref(next.Name) != "old name" || deref(original.Name) != "old name" {
        t.Fatal(next, applied, err)
    }
    for _, in := range []client.PSInput{{Since: new("yesterday")}, {Since: new("2026-10-29T00:00:00Z"), Until: new("2026-10-28T00:00:00Z")}, {Limit: new(0)}, {Target: new("bad host")}} {
        if err := uxDValidateFilters(in); err == nil {
            t.Fatal(in)
        }
    }
    if err := uxDValidateFilters(original); err != nil {
        t.Fatal("literal metadata rejected", err)
    }
    if width := ansi.StringWidth(uxDCell(strings.Repeat("中文", 100), 20)); width != 20 {
        t.Fatal(width)
    }
}

func TestUXDStopReviewFreezeAndConfirmedResult(t *testing.T) {
    o, remote, refs := psFixture(t)
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[stop]\ngrace='0s'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := o.Config.Remove(context.Background(), "lab"); err != nil {
        t.Fatal(err)
    }
    c := uxDCall(t, o, "en", "y\n")
    c.grammar.Target = new("replacement")
    v, err := c.uxDStop(context.Background(), refs[0].ID)
    if err != nil || v == nil || v.ID != refs[0].ID || remote.req.RunID != refs[0].ID || remote.req.Action != "stop" || remote.req.GraceMillis == nil || *remote.req.GraceMillis != 0 {
        t.Fatal(v, err, remote.req)
    }
    out := c.options.Err.(*bytes.Buffer).String() + c.uxDStopResult(*v, err).Title
    if !strings.Contains(out, "Terminal result confirmed") || !strings.Contains(out, "0s") || strings.Contains(out, "replacement") {
        t.Fatal(out)
    }
}

// Keep both cancellation and finite-timeout evidence through the existing Wait
// service; neither is translated into a stop or an execution failure.
func TestUXDWaitUsesFixedIDsAndRetainsPartialCoverage(t *testing.T) {
    o, remote, refs := psFixture(t)
    remote.failID = refs[0].ID
    c := uxDCall(t, o, "zh-Hans", "50ms\n1\n")
    result, err := c.uxDWait(context.Background(), []string{refs[0].ID, refs[1].ID})
    if err != nil || result.Selected != 2 || result.Completed != 1 || result.Pending != 1 || result.QueryErrors != 1 || !result.TimedOut || remote.req.Action != "" {
        t.Fatal(result, err, remote.req, c.options.Err)
    }
    out := c.options.Err.(*bytes.Buffer).String()
    if !strings.Contains(out, "观察预算已用完") || strings.Contains(out, "任务失败") {
        t.Fatal(out)
    }
}

type uxDRemote struct {
    execution.Remote
    target   config.Target
    request  wire.Request
    controls int
    code     string
    cancel   context.CancelFunc
}

func (r *uxDRemote) Control(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (*wire.RunRecord, error) {
    r.target, r.request = target, req
    r.controls++
    if r.code != "" {
        return nil, &ssh.RemoteError{Code: r.code}
    }
    return r.Remote.Control(ctx, target, runner, req, batch)
}

func (r *uxDRemote) Observe(ctx context.Context, target config.Target, runner string, req wire.Request, batch bool) (wire.Observation, error) {
    r.target, r.request = target, req
    if r.cancel != nil {
        r.cancel()
        <-ctx.Done()
        return wire.Observation{}, ctx.Err()
    }
    return r.Remote.Observe(ctx, target, runner, req, batch)
}

func TestUXDBoundStopUncertaintyRejectionAndCancellation(t *testing.T) {
    for _, code := range []string{"STOP_UNCONFIRMED", "BAD_REQUEST", "RUN_NOT_FOUND"} {
        o, base, refs := psFixture(t)
        remote := &uxDRemote{Remote: base, code: code}
        o.Runs.Remote = remote
        c := uxDCall(t, o, "en", "y\n")
        c.app.Projects.Settings.Targets = map[string]config.Target{"lab": {Destination: "replacement"}}
        v, err := c.uxDStop(context.Background(), refs[0].ID)
        var f *fault.Error
        if v == nil || !errors.As(err, &f) || f.Code != code || remote.controls != 1 || remote.request.RunID != refs[0].ID || remote.target.Destination != "original" || v.Status == "stopped" {
            t.Fatal(v, err, remote)
        }
        title := c.uxDStopResult(*v, err).Title
        if code == "STOP_UNCONFIRMED" && title != "Stop completion unconfirmed" || code != "STOP_UNCONFIRMED" && title != "Stop request failed" {
            t.Fatal(code, title)
        }
    }
    o, base, refs := psFixture(t)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    remote := &uxDRemote{Remote: base, cancel: cancel}
    o.Runs.Remote = remote
    c := uxDCall(t, o, "en", "30s\n")
    result, err := c.uxDWait(ctx, []string{refs[0].ID, refs[1].ID})
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "OBSERVATION_CANCELED" || result.Selected != 2 || len(result.Runs) != 2 || result.Completed+result.Pending != 2 || remote.controls != 0 {
        t.Fatal(result, err, remote)
    }
}
