package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "reflect"
    "slices"
    "strconv"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

type uxHQueueSpy struct {
    result       queue.Result
    listErr      error
    showErr      error
    mutationErr  error
    changed      queue.Result
    submits      []client.QueueSubmitInput
    lists        []client.QueueListInput
    shows        []client.QueueIDInput
    cancels      []client.QueueIDInput
    cleans       []client.QueueCleanInput
    enables      []client.QueueEnableInput
    pauses       []client.QueuePauseInput
    submitResult client.QueueSubmitResult
    submitErr    error
    onList       func()
}

func (s *uxHQueueSpy) List(_ context.Context, in client.QueueListInput) (queue.Result, error) {
    if in.Target != nil {
        frozen := *in.Target
        in.Target = &frozen
    }
    s.lists = append(s.lists, in)
    if s.onList != nil {
        s.onList()
    }
    return s.result, s.listErr
}
func (s *uxHQueueSpy) Show(_ context.Context, in client.QueueIDInput) (queue.Result, error) {
    s.shows = append(s.shows, in)
    return s.result, s.showErr
}
func (s *uxHQueueSpy) Cancel(_ context.Context, in client.QueueIDInput) (queue.Result, error) {
    s.cancels = append(s.cancels, in)
    return s.changed, s.mutationErr
}
func (s *uxHQueueSpy) Clean(_ context.Context, in client.QueueCleanInput) (queue.Result, error) {
    s.cleans = append(s.cleans, in)
    if in.DryRun {
        return s.result, s.showErr
    }
    return s.changed, s.mutationErr
}
func (s *uxHQueueSpy) Enable(_ context.Context, in client.QueueEnableInput) (queue.Result, error) {
    s.enables = append(s.enables, in)
    return s.changed, s.mutationErr
}
func (s *uxHQueueSpy) SetPaused(_ context.Context, in client.QueuePauseInput) (queue.Result, error) {
    s.pauses = append(s.pauses, in)
    return s.changed, s.mutationErr
}
func (s *uxHQueueSpy) Submit(_ context.Context, in client.QueueSubmitInput, _ client.ProgressFunc) (client.QueueSubmitResult, error) {
    s.submits = append(s.submits, in)
    if in.DryRun {
        return client.QueueSubmitResult{Stage: "planning", Preview: &execution.RunPreview{Target: deref(in.Target), Args: slices.Clone(in.Args), Metadata: task.RunMetadata{Name: in.Name, Tags: slices.Clone(in.Tags)}, Outputs: []string{"outputs"}, Notifications: slices.Clone(in.Notify), EnvironmentKeys: []string{"TOKEN"}, Source: sources.Plan{Policy: config.SyncPolicy{Mode: "mirror"}}}}, nil
    }
    return s.submitResult, s.submitErr
}

func uxHCallFixture(t *testing.T, language, input string, spy *uxHQueueSpy) *invocation {
    t.Helper()
    c := uxCoreCall(t, "")
    c.output.locale, _ = i18n.New(language, nil)
    uxHSetInput(c, input)
    c.grammar.Target = new("lab")
    c.grammar.Queue.List.Limit = 100
    c.app.Queue = spy
    return c
}

func uxHSetInput(c *invocation, input string) {
    c.input = interaction.New(interaction.Options{Interactive: true, In: strings.NewReader(input), Err: c.options.Err, ChoiceError: c.output.locale.Text("ux_core_choice_invalid")})
}

func TestUXHQueueObservationDoesNotEnableOrSubmit(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, state := range []string{"disabled", "empty", "paused", "full", "failed"} {
            t.Run(lang+"/"+state, func(t *testing.T) {
                spy := &uxHQueueSpy{result: queue.Result{Status: &queue.Status{TargetID: "shared", Slots: 2, CoordinatorRunning: true}}}
                if state == "disabled" {
                    spy.result, spy.listErr = queue.Result{}, fault.New("QUEUE_NOT_ENABLED", "queue_operation_failed", map[string]any{"Code": "QUEUE_NOT_ENABLED"})
                }
                if state == "failed" {
                    spy.listErr = fault.New("SSH_FAILED", "ssh_missing", nil)
                }
                if state == "paused" {
                    spy.result.Status.Paused = true
                }
                if state == "full" {
                    spy.result.Status.Reserved = 2
                }
                c := uxHCallFixture(t, lang, "1\n4\n", spy)
                if _, err := c.uxHQueueWith(context.Background(), uxHInputs{}); err != nil {
                    t.Fatal(err)
                }
                if len(spy.lists) != 2 || len(spy.submits)+len(spy.enables)+len(spy.pauses)+len(spy.cancels)+len(spy.cleans) != 0 {
                    t.Fatal("observation performed a mutation", spy)
                }
                text := c.options.Err.(*bytes.Buffer).String()
                if state == "failed" && strings.Contains(text, c.output.locale.Text("queue_empty")) {
                    t.Fatal("query failure became empty success", text)
                }
                if c.options.Out.(*bytes.Buffer).Len() != 0 {
                    t.Fatal("menus wrote stdout")
                }
            })
        }
    }
}

func TestUXHRequestActionsUseDurableOutcomeAndFixedIdentity(t *testing.T) {
    row := queue.Request{ID: "q_original", RunID: "r_fixed", State: "queued", ProjectID: "p_original"}
    for _, state := range []string{"canceled", "submitted", "dispatch_unconfirmed"} {
        t.Run(state, func(t *testing.T) {
            observed := row
            observed.State = state
            spy := &uxHQueueSpy{result: queue.Result{Request: &row}, changed: queue.Result{Request: &observed}}
            c := uxHCallFixture(t, "en", "y\n", spy)
            result, attempted, err := c.uxHMutateRequest(context.Background(), row, "cancel")
            if !attempted || len(spy.cancels) != 1 || spy.cancels[0].ID != row.ID || spy.cancels[0].Target != nil || result.Request.RunID != row.RunID {
                t.Fatal(attempted, err, spy)
            }
            if (err == nil) != (state == "canceled") {
                t.Fatal("cancel claimed success without no-start", result, err)
            }
            if len(spy.submits)+len(spy.pauses)+len(spy.enables) != 0 {
                t.Fatal("cancel escalated to other work")
            }
        })
    }
    for _, state := range []string{"dispatching", "submitted", "running", "completed", "unknown", "unconfirmed", "dispatch_unconfirmed"} {
        spy := &uxHQueueSpy{}
        c := uxHCallFixture(t, "en", "y\n", spy)
        row.State = state
        _, attempted, err := c.uxHMutateRequest(context.Background(), row, "cancel")
        if attempted || err == nil || len(spy.cancels) != 0 {
            t.Fatal(state, attempted, err)
        }
    }
}

func TestUXHCleanPreviewsConfirmsAndRetainsUncertainty(t *testing.T) {
    row := queue.Request{ID: "q_canceled", RunID: "r_fixed", State: "canceled"}
    for _, answer := range []string{"n", "y"} {
        spy := &uxHQueueSpy{result: queue.Result{Requests: []queue.Request{row}}, changed: queue.Result{Requests: []queue.Request{row}}, mutationErr: fault.New("QUEUE_CLEAN_UNCONFIRMED", "queue_operation_failed", nil)}
        c := uxHCallFixture(t, "en", "\n"+answer+"\n", spy)
        _, attempted, err := c.uxHMutateRequest(context.Background(), row, "clean")
        if len(spy.cleans) != 1+uxHBoolInt(answer == "y") || !spy.cleans[0].DryRun || !slices.Equal(spy.cleans[0].IDs, []string{row.ID}) {
            t.Fatal(spy.cleans)
        }
        if answer == "y" && (!attempted || uxHCode(err) != "QUEUE_CLEAN_UNCONFIRMED" || spy.cleans[1].DryRun) {
            t.Fatal(attempted, err, spy.cleans)
        }
        if answer == "n" && (attempted || err != nil) {
            t.Fatal(attempted, err)
        }
    }
}

func TestUXHEnablePauseAndResumeRequireExplicitChoice(t *testing.T) {
    for _, action := range []string{"enable", "pause", "resume", "capacity"} {
        spy := &uxHQueueSpy{changed: queue.Result{Status: &queue.Status{TargetID: "shared", Slots: 2, CoordinatorRunning: true}}}
        result := spy.changed
        var cause error
        input := "2\ny\n\n"
        if action == "enable" {
            result, cause = queue.Result{}, fault.New("QUEUE_NOT_ENABLED", "queue_operation_failed", nil)
            input = "2\n2\ny\n\n"
        } else if action == "capacity" {
            input = "3\n2\ny\n\n"
        } else if action == "resume" {
            result.Status.Paused = true
        }
        c := uxHCallFixture(t, "en", input, spy)
        if changed, err := c.uxHManage(context.Background(), "alias-for-shared", result, cause); err != nil || !changed {
            t.Fatal(action, err)
        }
        if action == "enable" || action == "capacity" {
            if len(spy.enables) != 1 || spy.enables[0].Slots != 2 || *spy.enables[0].Target != "alias-for-shared" {
                t.Fatal(spy.enables)
            }
        } else if len(spy.pauses) != 1 || spy.pauses[0].Paused != (action == "pause") {
            t.Fatal(spy.pauses)
        }
        if len(spy.submits)+len(spy.cancels)+len(spy.cleans) != 0 {
            t.Fatal("lifecycle touched workloads")
        }
    }
}

func TestUXHDetailKeepsPartialFactsAndNeverUsesCurrentProjectCommand(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        row := queue.Request{ID: "q_1", RunID: "r_1", State: "dispatch_unconfirmed", ReasonCode: "QUEUE_DISPATCH_UNCONFIRMED"}
        c := uxHCallFixture(t, language, "", &uxHQueueSpy{})
        result := queue.Result{Request: &row, StateError: "WRITE_FAILED"}
        if text := c.uxHSummary("lab", result, nil, 100); !strings.Contains(text, "WRITE_FAILED") {
            t.Fatal("local state failure disappeared without a query error", text)
        }
        text := c.uxHRequestDocument(result, fault.New("WRITE_FAILED", "state_write", nil)).Content(72, false, false)
        for _, want := range []string{"q_1", "r_1", "QUEUE_DISPATCH_UNCONFIRMED", "WRITE_FAILED", c.output.locale.Text("ux_h_command_unavailable")} {
            // Long prose may wrap; IDs/codes must remain present.
            if !strings.Contains(strings.Join(strings.Fields(text), ""), strings.Join(strings.Fields(want), "")) {
                t.Fatal(want, text)
            }
        }
        if len(c.uxHRunRoutes(row, false)) != 0 {
            t.Fatal("foreign request gained project-local actions")
        }
        if !strings.Contains(c.uxHState("future_state"), "future_state") {
            t.Fatal("unknown state lost")
        }
    }
}

func TestUXHRequestQueryFailureHidesMutationsAndForeignIsReadOnly(t *testing.T) {
    row := queue.Request{ID: "q_saved", RunID: "r_saved", State: "queued"}
    for _, local := range []bool{false, true} {
        spy := &uxHQueueSpy{showErr: fault.New("SSH_FAILED", "ssh_missing", nil)}
        c := uxHCallFixture(t, "en", "2\n", spy)
        if local {
            uxHSetInput(c, strconv.Itoa(3+len(c.uxHRunRoutes(row, true)))+"\n")
        }
        if err := c.uxHRequest(context.Background(), row, nil, local); err != nil {
            t.Fatal(err)
        }
        if len(spy.shows) != uxHBoolInt(local) || len(spy.cancels)+len(spy.cleans) != 0 {
            t.Fatal(spy)
        }
        text := c.options.Err.(*bytes.Buffer).String()
        if strings.Contains(text, c.output.locale.Text("ux_h_cancel")) || strings.Contains(text, c.output.locale.Text("ux_h_clean")) {
            t.Fatal("failed observation offered mutation", text)
        }
    }
}

func TestUXHConsumesMachinePickerWithoutChangingDefaults(t *testing.T) {
    spy := &uxHQueueSpy{result: queue.Result{Status: &queue.Status{Slots: 1, CoordinatorRunning: true}}}
    c := uxHCallFixture(t, "en", "4\n1\n5\n", spy)
    for _, name := range []string{"lab", "alias"} {
        if err := c.app.Machines.Config.Add(context.Background(), name, config.Target{Destination: "same-host", RemoteBase: "/work"}, false); err != nil {
            t.Fatal(err)
        }
    }
    settings, err := c.app.Machines.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    c.app.Machines.Settings = settings
    before, _ := os.ReadFile(c.app.Machines.Config.ConfigFile)
    if _, err := c.uxHQueueWith(context.Background(), uxHInputs{pickTarget: c.uxAPickTarget}); err != nil {
        t.Fatal(err)
    }
    after, _ := os.ReadFile(c.app.Machines.Config.ConfigFile)
    if !bytes.Equal(before, after) || *c.grammar.Target != "lab" || len(spy.lists) != 2 || *spy.lists[0].Target != "lab" || *spy.lists[1].Target != "alias" {
        t.Fatal("visit selection changed a default or identity", spy.lists)
    }
    if _, err := os.Stat(c.app.Machines.State.Path); !os.IsNotExist(err) {
        t.Fatal("picker created personal project state", err)
    }
}

func TestUXHDirectJSONAndNoninteractiveRetainSingleResult(t *testing.T) {
    for _, args := range [][]string{{"queue"}, {"queue", "--json"}, {"queue", "list", "--json"}, {"queue", "submit", "--dry-run", "--json", "--", "printf", "a b"}} {
        o := fixture(t)
        spy := &fakeQueue{}
        o.Queue, o.In = spy, uxCoreNoInput{t}
        code, stdout, stderr := run(t, o, args...)
        if code != 0 || stderr != "" {
            t.Fatal(args, code, stdout, stderr)
        }
        if slices.Contains(args, "--json") {
            decoder := json.NewDecoder(strings.NewReader(stdout))
            var env envelope
            if err := decoder.Decode(&env); err != nil || !env.OK {
                t.Fatal(stdout, err)
            }
            if err := decoder.Decode(&env); !errors.Is(err, io.EOF) {
                t.Fatal("multiple finite results", stdout, err)
            }
        }
    }
    for _, jsonMode := range []bool{true, false} {
        c := uxHCallFixture(t, "en", "", &uxHQueueSpy{})
        c.policy.Batch, c.output.json = !jsonMode, jsonMode
        if _, err := c.uxHQueue(context.Background()); !errors.Is(err, interaction.ErrNonInteractive) {
            t.Fatal(err)
        }
        if _, err := c.uxHSubmit(context.Background()); !errors.Is(err, interaction.ErrNonInteractive) {
            t.Fatal(err)
        }
    }
}

func TestUXHFilterPreservesIdentityOrderAndControls(t *testing.T) {
    rows := []queue.Request{{ID: "q_2", RunID: "r_2", Name: "实验", Tags: []string{"seed 1"}}, {ID: "q_1", State: "queued"}}
    before := slices.Clone(rows)
    if found := uxHFilter(rows, "SEED 1"); len(found) != 1 || found[0].ID != "q_2" {
        t.Fatal(found)
    }
    if !reflect.DeepEqual(before, rows) || strings.ContainsAny(uxHText("bad\x1b\nname"), "\x1b\n") {
        t.Fatal("presentation mutated data or emitted controls")
    }
}

func uxHBoolInt(v bool) int {
    if v {
        return 1
    }
    return 0
}

func TestUXHSavedPreparationCanRequestDurableNoStart(t *testing.T) {
    seed := queue.Request{ID: "q_saved", RunID: "r_fixed", State: "preparing"}
    canceled := seed
    canceled.State = "canceled"
    spy := &uxHQueueSpy{showErr: fault.New("QUEUE_NOT_FOUND", "queue_operation_failed", map[string]any{"Code": "QUEUE_NOT_FOUND"}), changed: queue.Result{Request: &canceled}}
    c := uxHCallFixture(t, "en", "", spy)
    uxHSetInput(c, "3\ny\n\n"+strconv.Itoa(3+len(c.uxHRunRoutes(seed, true)))+"\n")
    if err := c.uxHRequest(context.Background(), seed, nil, true); err != nil {
        t.Fatal(err)
    }
    if len(spy.cancels) != 1 || spy.cancels[0].ID != seed.ID || spy.cancels[0].Target != nil {
        t.Fatal("failed preparation lost explicit no-start recovery", spy.cancels)
    }
}

func TestUXHQueueExplicitZeroLimitDoesNotBecomeDefault(t *testing.T) {
    spy := &uxHQueueSpy{}
    c := uxHCallFixture(t, "en", "", spy)
    c.grammar.Queue.List.Limit = 0
    _, err := c.uxHQueue(context.Background())
    if uxHCode(err) != "BAD_CONFIG" || len(spy.lists) != 0 {
        t.Fatal("explicit zero was replaced by a default or queried", err, spy.lists)
    }
}
