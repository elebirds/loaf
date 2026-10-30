package cli

import (
    "bytes"
    "context"
    "errors"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "slices"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type uxFListCall struct {
    ref            task.RunRef
    depth, limit   int
    prepare, batch bool
}
type uxFBrowser struct {
    calls                                  []uxFListCall
    requireHelper, empty, missing, limited bool
    fail                                   error
}

func (b *uxFBrowser) List(_ context.Context, ref task.RunRef, depth, limit int, prepare, batch bool) (execution.OutputListing, error) {
    b.calls = append(b.calls, uxFListCall{ref, depth, limit, prepare, batch})
    result := execution.OutputListing{RunID: ref.ID, Target: ref.Target, Paths: ref.Outputs, OutputInventory: wire.OutputInventory{Depth: depth, Limit: limit, RootsTotal: len(ref.Outputs), RootsExamined: len(ref.Outputs)}}
    if b.requireHelper && !prepare {
        return result, fault.New("RUNNER_REQUIRED", "outputs_runner_required", nil)
    }
    if b.fail != nil {
        return result, b.fail
    }
    if !b.empty {
        result.Entries = []wire.OutputEntry{{Path: "outputs", Type: "directory"}, {Path: "outputs/中文 space[1]*?,.csv", Type: "file", Size: 42, ModifiedAt: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}}
        result.Examined = len(result.Entries)
    }
    if b.limited {
        result.Truncated = true
        result.DepthLimited = 2
    }
    if b.missing {
        result.Missing = []string{"checkpoints"}
        return result, fault.New("OUTPUT_MISSING", "pull_missing", nil)
    }
    return result, nil
}

func uxFCall(t *testing.T, language, input string) (*invocation, *uxFBrowser, *fakePuller, []task.RunRef) {
    t.Helper()
    o, _, refs := psFixture(t)
    browser, puller := &uxFBrowser{}, &fakePuller{}
    o.Outputs, o.Puller = browser, puller
    settings, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    l, err := i18n.New(language, nil)
    if err != nil {
        t.Fatal(err)
    }
    o.In = strings.NewReader(input)
    o.Interactive = true
    c := &invocation{ui: true, app: client.New(o.Dependencies, settings), options: o.Options, grammar: &grammar{}, output: &presenter{locale: l, out: o.Out, err: o.Err}}
    c.input = interaction.New(interaction.Options{In: o.In, Err: o.Err, Interactive: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help"), MultiHelp: l.Text("ux_core_multi_help"), PlainMultiHelp: l.Text("ux_core_multi_plain")})
    return c, browser, puller, refs
}

func TestUXFOutputBrowserReadOnlyBindingsBoundsAndLanguages(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        t.Run(lang, func(t *testing.T) {
            c, b, p, refs := uxFCall(t, lang, "6\n0\n1\n7\n")
            c.app.Projects.Settings.Targets["lab"] = config.Target{Destination: "replacement"}
            if _, err := c.uxFOutputs(context.Background(), refs[0].ID); err != nil {
                t.Fatal(err)
            }
            if len(b.calls) != 2 || b.calls[0].prepare || b.calls[1].prepare || b.calls[1].depth != 0 || b.calls[1].limit != 1 || b.calls[0].ref.Connection.Destination != "original" || b.calls[0].ref.ID != refs[0].ID || p.calls != 0 {
                t.Fatal(b, p)
            }
            if c.options.Out.(*bytes.Buffer).Len() != 0 {
                t.Fatal("menu wrote stdout")
            }
            if c.app.Projects.Runs.Remote.(*psRemote).calls != 0 {
                t.Fatal("browser queried task status")
            }
        })
    }
}

func TestUXFHelperPreparationRequiresChoiceAndConfirmation(t *testing.T) {
    for _, test := range []struct {
        name, input string
        calls       int
        prepared    bool
    }{{"back", "7\n", 1, false}, {"decline", "4\nn\n7\n", 1, false}, {"approve", "4\ny\n7\n", 2, true}} {
        t.Run(test.name, func(t *testing.T) {
            c, b, p, refs := uxFCall(t, "en", test.input)
            b.requireHelper = true
            if _, err := c.uxFOutputs(context.Background(), refs[0].ID); err != nil {
                t.Fatal(err)
            }
            if len(b.calls) != test.calls || b.calls[len(b.calls)-1].prepare != test.prepared || p.calls != 0 {
                t.Fatal(b, p)
            }
        })
    }
}

func TestUXFEmptyMissingDepthLimitAndCleanedViews(t *testing.T) {
    for _, mode := range []string{"empty", "missing", "limited", "cleaned"} {
        t.Run(mode, func(t *testing.T) {
            input := "7\n"
            if mode == "cleaned" {
                input = "6\n"
            }
            c, b, p, refs := uxFCall(t, "en", input)
            switch mode {
            case "empty":
                b.empty = true
            case "missing":
                b.missing = true
            case "limited":
                b.limited = true
            case "cleaned":
                b.fail = fault.New("RUN_CLEANED", "clean_run_cleaned", nil)
            }
            if _, err := c.uxFOutputs(context.Background(), refs[0].ID); err != nil {
                t.Fatal(err)
            }
            text := c.options.Err.(*bytes.Buffer).String()
            want := map[string]string{"empty": "No entries", "missing": "missing output roots", "limited": "Partial listing", "cleaned": "RUN_CLEANED"}[mode]
            if !strings.Contains(text, want) || p.calls != 0 || len(b.calls) != 1 {
                t.Fatal(text, b, p)
            }
        })
    }
}

func TestUXFLiteralChoicesRejectLinksUnknownAndEscapes(t *testing.T) {
    r := execution.OutputListing{Paths: []string{"outputs/"}, OutputInventory: wire.OutputInventory{Entries: []wire.OutputEntry{
        {Path: "outputs/中文 space[1]*?,.csv", Type: "file"}, {Path: "outputs/sub", Type: "directory"}, {Path: "outputs/link", Type: "symlink"},
        {Path: "outputs/pipe", Type: "unknown"}, {Path: "outputs/../escape", Type: "file"}, {Path: "elsewhere/data", Type: "file"}, {Path: "outputs/sub", Type: "directory"},
    }}}
    choices := uxFOutputChoices(r)
    if len(choices) != 2 || choices[0].Value != "outputs/中文 space[1]*?,.csv" || choices[1].Value != "outputs/sub" {
        t.Fatal(choices)
    }
}

func TestUXFPathPagesRetainSelectionsOnPreviousAndBack(t *testing.T) {
    c, _, _, _ := uxFCall(t, "en", "1\n2\n1\n2\n\n4\n")
    choices := []interaction.Choice{}
    for _, name := range []string{"first", "two", "three", "four", "five", "last"} {
        choices = append(choices, interaction.Choice{Label: name, Value: "outputs/" + name})
    }
    selected, err := c.uxFSelectPaths(context.Background(), choices, nil, "help")
    if err != nil || len(selected) != 2 || !slices.Contains(selected, "outputs/first") || !slices.Contains(selected, "outputs/last") {
        t.Fatal(selected, err)
    }
}

func TestUXFOutputSelectionDownloadAndBackPreservesLiteralPaths(t *testing.T) {
    // Pick the file, keep it, review, explicitly download, leave the result,
    // then leave the browser. No manual path entry or shell interpretation.
    c, b, p, refs := uxFCall(t, "en", "2\n2\n1\n3\n2\n2\n7\n")
    if _, err := c.uxFOutputs(context.Background(), refs[0].ID); err != nil {
        t.Fatal(err)
    }
    if len(b.calls) != 1 || p.calls != 1 || p.batch || !reflect.DeepEqual(p.paths, []string{"outputs/中文 space[1]*?,.csv"}) || filepath.Base(p.destination) != refs[0].ID || p.policy.Checksum {
        t.Fatal(b, p)
    }
    text := c.options.Err.(*bytes.Buffer).String()
    if !strings.Contains(text, c.output.locale.Text("ux_f_selection_count", map[string]any{"Count": 1})) || !strings.Contains(text, strconv.Quote(p.destination)) {
        t.Fatal(text)
    }
}

func TestUXFReviewExplicitDestinationAndChecksumFalse(t *testing.T) {
    c, _, p, refs := uxFCall(t, "en", "3\n1\nexact output\n4\n2\n2\n2\n")
    c.app.Projects.Settings.Pull.Checksum = new(true)
    if err := c.uxFDownload(context.Background(), client.PullInput{IDs: []string{refs[0].ID}}); err != nil {
        t.Fatal(err)
    }
    root, err := filepath.EvalSymlinks(c.app.Projects.CWD)
    if err != nil {
        t.Fatal(err)
    }
    if p.calls != 1 || p.policy.Checksum || p.destination != filepath.Join(root, "exact output") {
        t.Fatal(p)
    }
}

type uxFRetryPuller struct {
    calls  []string
    fail   string
    cancel context.CancelFunc
}

func (p *uxFRetryPuller) Pull(_ context.Context, ref task.RunRef, destination string, paths []string, policy config.PullPolicy, _ bool) (sources.PullResult, error) {
    p.calls = append(p.calls, ref.ID)
    r := sources.PullResult{RunID: ref.ID, Target: ref.Target, Destination: destination, Paths: paths, Policy: policy}
    if p.cancel != nil {
        p.cancel()
        return r, fault.New("PULL_CANCELED", "pull_canceled", nil)
    }
    if ref.ID == p.fail {
        p.fail = ""
        r.MissingPaths = []string{"checkpoints"}
        return r, fault.New("OUTPUT_MISSING", "pull_missing", nil)
    }
    n := 1
    r.ChangedFiles = &n
    r.DownloadedPaths = paths
    return r, nil
}

func TestUXFBatchRetryRetainsSuccessfulRowsAndOriginalDestinations(t *testing.T) {
    c, _, _, refs := uxFCall(t, "en", "2\n2\n")
    puller := &uxFRetryPuller{fail: refs[1].ID}
    c.app.Projects.Puller = puller
    plan, err := c.app.Projects.PreviewPull(context.Background(), client.PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: new("comparison")})
    if err != nil {
        t.Fatal(err)
    }
    if err := c.uxFPerformPull(context.Background(), plan.Input); err != nil {
        t.Fatal(err)
    }
    if !reflect.DeepEqual(puller.calls, []string{refs[0].ID, refs[1].ID, refs[1].ID}) {
        t.Fatal(puller.calls)
    }
    text := c.options.Err.(*bytes.Buffer).String()
    if !strings.Contains(text, "2 completed · 0 failed") || !strings.Contains(text, strconv.Quote(*plan.Input.Into)) || !strings.Contains(text, "PULL_PARTIAL") {
        t.Fatal(text)
    }
}

func TestUXFCancelPreservesBatchCoverageAndFailedTransferHasNoCount(t *testing.T) {
    c, _, _, refs := uxFCall(t, "en", "")
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    puller := &uxFRetryPuller{cancel: cancel}
    c.app.Projects.Puller = puller
    err := c.uxFPerformPull(ctx, client.PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: new("comparison")})
    text := c.options.Err.(*bytes.Buffer).String()
    if err == nil || len(puller.calls) != 1 || !strings.Contains(text, "NOT_EXAMINED") || !strings.Contains(text, "PULL_CANCELED") || strings.Contains(text, "Changed files") || strings.Contains(text, "0 failed") {
        t.Fatal(err, text)
    }
}

type uxFFailingWriter struct{}

func (uxFFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestUXFResultWriterFailureAndNoninteractiveNeverExecute(t *testing.T) {
    c, _, p, refs := uxFCall(t, "en", "")
    c.output.err = uxFFailingWriter{}
    if err := c.uxFWritePullResult(client.PullResult{Single: &sources.PullResult{RunID: refs[0].ID}}, fault.New("PULL_FAILED", "pull_failed", nil)); !errors.Is(err, io.ErrClosedPipe) {
        t.Fatal(err)
    }
    c.policy.Batch = true
    if _, err := c.uxFOutputs(context.Background(), refs[0].ID); !errors.Is(err, interaction.ErrNonInteractive) {
        t.Fatal(err)
    }
    if _, err := c.uxFResults(context.Background()); !errors.Is(err, interaction.ErrNonInteractive) {
        t.Fatal(err)
    }
    if p.calls != 0 {
        t.Fatal("batch mode entered a menu")
    }
}

func TestUXFFullDirectCommandsStayFiniteAndDoNotPrompt(t *testing.T) {
    o, _, refs := psFixture(t)
    o.Outputs = &uxFBrowser{missing: true}
    o.Puller = &fakePuller{missing: true}
    o.Interactive = true
    o.In = uxCoreNoInput{t}
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{{"outputs", refs[0].ID, "--depth", "1", "--limit", "5", "--json"}, {"pull", refs[0].ID, "--to", "direct", "--checksum=false", "--json"}} {
            code, out, stderr := run(t, o, append([]string{"--lang", lang}, args...)...)
            if code != 1 || stderr != "" || !strings.Contains(out, `"code":"OUTPUT_MISSING"`) || strings.Count(strings.TrimSpace(out), "\n") != 0 {
                t.Fatal(code, out, stderr)
            }
        }
    }
    if _, err := os.Stat(filepath.Join(o.CWD, "direct")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("fake transfer wrote destination", err)
    }
}

func TestUXFResultsConsumesDLocalSelectionAndExactOutputRoute(t *testing.T) {
    c, b, p, refs := uxFCall(t, "en", "1\n5\n7\n")
    if !c.uxRouteAvailable("results") || !c.uxRouteAvailable("outputs") {
        t.Fatal("F routes not integrated")
    }
    if data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "results"}); err != nil || data != nil {
        t.Fatal(data, err)
    }
    if len(b.calls) != 1 || b.calls[0].ref.ID != refs[0].ID || p.calls != 0 || c.app.Projects.Runs.Remote.(*psRemote).calls != 0 {
        t.Fatal(b, p)
    }
    c, b, p, refs = uxFCall(t, "zh-Hans", "7\n")
    c.grammar.Target = new("replacement")
    if _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "outputs", ID: refs[1].ID}); err != nil {
        t.Fatal(err)
    }
    if len(b.calls) != 1 || b.calls[0].ref.ID != refs[1].ID || b.calls[0].ref.Connection.Destination != "original" || p.calls != 0 || *c.grammar.Target != "replacement" {
        t.Fatal(b, p)
    }
}

func TestUXFResultsMultiSelectionDoesNotQueryBeforeBrowseOrDownload(t *testing.T) {
    c, b, p, _ := uxFCall(t, "en", "1\n2\n5\n8\n")
    if _, err := c.uxFResults(context.Background()); err != nil {
        t.Fatal(err)
    }
    if len(b.calls) != 0 || p.calls != 0 || c.app.Projects.Runs.Remote.(*psRemote).calls != 0 {
        t.Fatal(b, p)
    }
}

func TestUXFDownloadRevalidationFailureNeverBecomesCompletedRows(t *testing.T) {
    c, _, p, refs := uxFCall(t, "en", "")
    plan, err := c.app.Projects.PreviewPull(context.Background(), client.PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: new("comparison")})
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(*plan.Input.Into, []byte("another writer created a file"), 0600); err != nil {
        t.Fatal(err)
    }
    err = c.uxFPerformPull(context.Background(), plan.Input)
    if err == nil || p.calls != 0 || strings.Contains(c.options.Err.(*bytes.Buffer).String(), "completed") {
        t.Fatal(err, p)
    }
}
