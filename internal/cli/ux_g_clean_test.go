package cli

import (
    "bytes"
    "context"
    "errors"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

type uxGMenuCleaner struct {
    fakeCleaner
    refs    []task.RunRef
    repairs []bool
    cleaned []string
    onClean func()
}

func (c *uxGMenuCleaner) Preview(ctx context.Context, ref task.RunRef, batch, repair bool) (execution.CleanResult, error) {
    c.refs = append(c.refs, ref)
    c.repairs = append(c.repairs, repair)
    return c.fakeCleaner.Preview(ctx, ref, batch, repair)
}

func (c *uxGMenuCleaner) Clean(ctx context.Context, item execution.CleanResult, batch bool) (execution.CleanResult, error) {
    c.cleaned = append(c.cleaned, item.Run.ID)
    if c.onClean != nil {
        c.onClean()
    }
    return c.fakeCleaner.Clean(ctx, item, batch)
}

func uxGMenuFixture(t *testing.T, language, input string, count int) (*invocation, *uxGMenuCleaner, []string) {
    t.Helper()
    o, remote := runFixture(t)
    ids := []string{}
    for i := 0; i < count; i++ {
        if code, out, stderr := run(t, o, "run", "--json", "--", "printf", "fixture"); code != 0 {
            t.Fatal(code, out, stderr)
        }
        ids = append(ids, remote.req.RunID)
    }
    cleaner := &uxGMenuCleaner{}
    o.Cleaner = cleaner
    o.In, o.Interactive = strings.NewReader(input), true
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l, err := i18n.New(language, nil)
    if err != nil {
        t.Fatal(err)
    }
    c := &invocation{ui: true, app: client.New(o.Dependencies, settings), options: o.Options, grammar: &grammar{}, output: &presenter{locale: l, out: o.Out, err: o.Err}}
    c.input = interaction.New(interaction.Options{In: o.In, Err: o.Err, Interactive: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help")})
    return c, cleaner, ids
}

func TestUXGMenuDefaultCancelAndDefaultNoNeverClean(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, input := range []string{"\n\n", "\n1\n\n", "\n1\nno\n"} {
            c, cleaner, ids := uxGMenuFixture(t, language, input, 1)
            data, err := c.uxGClean(context.Background(), ids)
            if err != nil || data != nil || cleaner.cleans != 0 || !reflect.DeepEqual(cleaner.repairs, []bool{false}) {
                t.Fatal(data, err, cleaner)
            }
            output := c.options.Err.(*bytes.Buffer).String()
            if !strings.Contains(output, ids[0]) || !strings.Contains(output, "lab") || !strings.Contains(output, cleaner.refs[0].RemoteDir+"/runs/"+ids[0]) || c.options.Out.(*bytes.Buffer).Len() != 0 {
                t.Fatal(output)
            }
            if strings.HasPrefix(input, "\n1\n") && (strings.Count(output, "[y/N]") != 1 || strings.Count(output, c.output.locale.Text("ux_g_scope")) != 1) {
                t.Fatal("confirmation repeated the deletion scope", output)
            }
        }
    }
}

func TestUXGMenuExplicitConsentDeduplicatesAndKeepsIdentity(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        c, cleaner, ids := uxGMenuFixture(t, language, "\n1\nyes\n\n", 1)
        data, err := c.uxGClean(context.Background(), []string{ids[0], ids[0]})
        if err != nil || data != nil || cleaner.previews != 1 || !reflect.DeepEqual(cleaner.cleaned, ids) || cleaner.repairs[0] {
            t.Fatal(data, err, cleaner)
        }
        output := c.options.Err.(*bytes.Buffer).String()
        for _, text := range []string{c.output.locale.Text("clean_status_cleaned"), c.output.locale.Text("ux_g_retained"), ids[0]} {
            if !strings.Contains(output, text) {
                t.Fatal("missing result", text, output)
            }
        }
    }
}

func TestUXGSelectionEditsRequireANewCompletePreview(t *testing.T) {
    c, cleaner, ids := uxGMenuFixture(t, "en", "\n1\n\n1\nyes\n\n", 2)
    cleaner.blockedID = ids[1]
    selections := 0
    data, err := c.uxGCleanFlow(context.Background(), nil, func(_ context.Context, multiple bool) ([]string, error) {
        if !multiple {
            t.Fatal("cleanup requested an implicit single selection")
        }
        selections++
        if selections == 1 {
            return ids, nil
        }
        return ids[:1], nil
    })
    if err != nil || data != nil || selections != 2 || cleaner.previews != 3 || !reflect.DeepEqual(cleaner.cleaned, ids[:1]) {
        t.Fatal(data, err, selections, cleaner)
    }
    output := c.options.Err.(*bytes.Buffer).String()
    if !strings.Contains(output, ids[1]) || !strings.Contains(output, "CLEAN_NOT_TERMINAL") {
        t.Fatal("blocked row was hidden", output)
    }
}

func TestUXGRefreshKeepsExactIDsAndDoesNotPrepareHelper(t *testing.T) {
    c, cleaner, ids := uxGMenuFixture(t, "en", "\n4\n\n\n", 2)
    data, err := c.uxGClean(context.Background(), ids[:1])
    if err != nil || data != nil || cleaner.cleans != 0 || cleaner.previews != 2 {
        t.Fatal(data, err, cleaner)
    }
    for i, ref := range cleaner.refs {
        if ref.ID != ids[0] || cleaner.repairs[i] {
            t.Fatal("refresh expanded scope or prepared a helper", cleaner)
        }
    }
}

func TestUXGMenuUsesDLocalSelectionBeforePreview(t *testing.T) {
    c, cleaner, ids := uxGMenuFixture(t, "en", "1\n2\n\n1\nyes\n\n", 1)
    data, err := c.uxGClean(context.Background(), nil)
    if err != nil || data != nil || cleaner.previews != 1 || !reflect.DeepEqual(cleaner.cleaned, ids) || cleaner.repairs[0] {
        t.Fatal(data, err, cleaner)
    }
}

func TestUXGUnconfirmedRecoveryDoesNotReplayWithoutNewConsent(t *testing.T) {
    c, cleaner, ids := uxGMenuFixture(t, "en", "\n1\nyes\n\n1\n\n\n", 2)
    cleaner.unconfirmedID = ids[1]
    data, err := c.uxGClean(context.Background(), ids)
    if err != nil || data != nil || !reflect.DeepEqual(cleaner.cleaned, ids) || cleaner.previews != 3 || cleaner.refs[2].ID != ids[1] {
        t.Fatal(data, err, cleaner)
    }
    output := c.options.Err.(*bytes.Buffer).String()
    if !strings.Contains(output, c.output.locale.Text("clean_status_pending")) || !strings.Contains(output, c.output.locale.Text("clean_status_cleaned")) {
        t.Fatal(output)
    }
}

func TestUXGCancellationReportsEveryUnattemptedRow(t *testing.T) {
    c, cleaner, ids := uxGMenuFixture(t, "zh-Hans", "\n1\nyes\n", 3)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    cleaner.onClean = cancel
    cleaner.unconfirmedID = ids[0]
    _, err := c.uxGClean(ctx, ids)
    if !errors.Is(err, context.Canceled) || cleaner.cleans != 1 {
        t.Fatal(err, cleaner)
    }
    output := c.options.Err.(*bytes.Buffer).String()
    for _, id := range ids {
        if !strings.Contains(output, id) {
            t.Fatal("cancellation lost coverage", output)
        }
    }
    if !strings.Contains(output, c.output.locale.Text("ux_g_not_attempted")) || !strings.Contains(output, c.output.locale.Text("clean_status_pending")) {
        t.Fatal(output)
    }
}

func TestUXGMenuMissingSelectionAndBatchFailBeforeInput(t *testing.T) {
    c, cleaner, ids := uxGMenuFixture(t, "en", "", 1)
    _, err := c.uxGCleanFlow(context.Background(), nil, nil)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "BAD_CONFIG" || cleaner.previews != 0 {
        t.Fatal(err, cleaner)
    }
    for _, mode := range []string{"batch", "json"} {
        c.policy.Batch, c.output.json = mode == "batch", mode == "json"
        if _, err := c.uxGClean(context.Background(), ids); !errors.Is(err, interaction.ErrNonInteractive) || cleaner.previews != 0 {
            t.Fatal(mode, err, cleaner)
        }
    }
}

func TestUXGResultReasonsAndSafeNextActions(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(language, nil)
        for _, code := range []string{"CLEAN_NOT_TERMINAL", "CLEAN_NOT_CONFIRMED", "CLEAN_BUSY", "BAD_FORMAT", "PROJECT_MISMATCH", "RUN_NOT_FOUND", "VERSION_MISMATCH", "CLEAN_CANCELED", "SSH_FAILED"} {
            _, reason := uxGCleanState(execution.CleanResult{ErrorCode: code}, true, l)
            if !strings.Contains(reason, code) || strings.TrimSpace(reason) == "("+code+")" {
                t.Fatal(code, reason)
            }
        }
        batch := client.CleanBatch{Runs: []execution.CleanResult{
            {Run: execution.View{ID: "r_done"}, Cleaned: true},
            {Run: execution.View{ID: "r_pending", CleanupPending: true}, ErrorCode: "CLEAN_UNCONFIRMED"},
            {Run: execution.View{ID: "r_unattempted"}, ErrorCode: "CLEAN_NOT_ATTEMPTED"},
        }}
        actions := uxGNextActions(batch)
        if len(actions) != 4 || !actions[3].RequiresConfirmation || !reflect.DeepEqual(actions[3].Command, []string{"loaf", "clean", "r_pending", "r_unattempted"}) {
            t.Fatal(actions)
        }
        state, _ := uxGCleanState(batch.Runs[2], false, l)
        if state != l.Text("ux_g_not_attempted") {
            t.Fatal(state)
        }
        if !reflect.DeepEqual(uxGRecoveryIDs(batch), []string{"r_pending", "r_unattempted"}) {
            t.Fatal(uxGRecoveryIDs(batch))
        }
    }
}

type uxGOutputFlow struct {
    refs  []task.RunRef
    pulls int
}

func (f *uxGOutputFlow) List(_ context.Context, ref task.RunRef, _, _ int, prepare, _ bool) (execution.OutputListing, error) {
    if prepare {
        return execution.OutputListing{}, errors.New("unexpected helper preparation")
    }
    f.refs = append(f.refs, ref)
    return execution.OutputListing{RunID: ref.ID, Target: ref.Target, Paths: append([]string(nil), ref.Outputs...)}, nil
}

func (f *uxGOutputFlow) Pull(context.Context, task.RunRef, string, []string, config.PullPolicy, bool) (sources.PullResult, error) {
    f.pulls++
    return sources.PullResult{}, errors.New("unexpected automatic download")
}

func TestUXGDownloadFirstUsesFWithTheChosenIDAndNoAutomaticAction(t *testing.T) {
    c, cleaner, ids := uxGMenuFixture(t, "en", "\n2\n1\n7\n\n\n", 2)
    outputs := &uxGOutputFlow{}
    c.app.Projects.Outputs, c.app.Projects.Puller = outputs, outputs
    c.grammar.Target = new("replacement")
    data, err := c.uxGClean(context.Background(), ids[:1])
    if err != nil || data != nil || cleaner.cleans != 0 || outputs.pulls != 0 || len(outputs.refs) != 1 || outputs.refs[0].ID != ids[0] || outputs.refs[0].Target != "lab" || cleaner.previews != 2 {
        t.Fatal(data, err, outputs, cleaner)
    }
    for _, ref := range cleaner.refs {
        if ref.ID != ids[0] {
            t.Fatal("return from outputs expanded cleanup selection", ref)
        }
    }
}

func TestUXGDirectCleanEmptyIDNeverSelectsLatest(t *testing.T) {
    o, remote := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "printf", "fixture"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    cleaner := &uxGMenuCleaner{}
    o.Cleaner = cleaner
    for _, ids := range [][]string{{""}, {remote.req.RunID, ""}} {
        args := append([]string{"clean"}, ids...)
        code, out, stderr := run(t, o, append(args, "--yes", "--json")...)
        if code != 2 || stderr != "" || !strings.Contains(out, `"code":"BAD_CONFIG"`) || cleaner.previews != 0 || cleaner.cleans != 0 {
            t.Fatal(code, out, stderr, cleaner)
        }
    }
}
