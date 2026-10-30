package cli

import (
    "bytes"
    "context"
    "slices"
    "strconv"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/queue"
)

func TestUXHSubmitConsumesCommandEditorAndNeverReplaysUncertainty(t *testing.T) {
    row := queue.Request{ID: "q_saved", RunID: "r_fixed", State: "dispatch_unconfirmed"}
    spy := &uxHQueueSpy{result: queue.Result{Request: &row}, submitResult: client.QueueSubmitResult{Stage: "enqueueing", NextAction: "inspect_original_request", Request: &row}, submitErr: fault.New("QUEUE_ENQUEUE_UNCONFIRMED", "queue_operation_failed", map[string]any{"Code": "QUEUE_ENQUEUE_UNCONFIRMED"})}
    // Edit -> keep literal argv -> review -> back from preview -> confirm ->
    // back from receipt -> refresh same request -> return. No second submission.
    c := uxHCallFixture(t, "en", "", spy)
    uxHSetInput(c, "2\n1\n1\n\ny\n\n2\n"+strconv.Itoa(3+len(c.uxHRunRoutes(row, true)))+"\n")
    args := []string{"python3", "train space.py", "$(literal)", "", "--json"}
    c.grammar.Queue.Submit.Argv = append([]string{"--"}, args...)
    c.grammar.Queue.Submit.Name = "Experiment"
    c.grammar.Queue.Submit.Tag = []string{"seed 1", "baseline"}
    c.grammar.Queue.Submit.Notify = []string{"chosen"}
    if _, err := c.uxHSubmitWith(context.Background(), uxHInputs{editCommand: c.uxCEditCommand}); err != nil {
        t.Fatal(err)
    }
    if len(spy.submits) != 2 || !spy.submits[0].DryRun || spy.submits[1].DryRun || !slices.Equal(spy.submits[1].Args, args) || !slices.Equal(spy.submits[1].Tags, []string{"seed 1", "baseline"}) || !slices.Equal(spy.submits[1].Notify, []string{"chosen"}) {
        t.Fatal(spy.submits)
    }
    if len(spy.shows) != 2 || spy.shows[0].ID != row.ID || spy.shows[1].ID != row.ID || spy.shows[0].Target != nil || len(spy.enables)+len(spy.pauses) != 0 {
        t.Fatal("submission changed identity or enabled queue", spy)
    }
    text := c.options.Err.(*bytes.Buffer).String()
    if !strings.Contains(text, "r_fixed") || !strings.Contains(text, "QUEUE_ENQUEUE_UNCONFIRMED") {
        t.Fatal("lost recovery identity", text)
    }
}

func TestUXHSubmitDryRunAndDeclineNeverTouchQueue(t *testing.T) {
    for _, dry := range []bool{true, false} {
        spy := &uxHQueueSpy{}
        input := "1\n\n"
        if !dry {
            input += "n\n4\n"
        }
        c := uxHCallFixture(t, "en", input, spy)
        c.grammar.Queue.Submit.DryRun = dry
        if _, err := c.uxHSubmitWith(context.Background(), uxHInputs{}); err != nil {
            t.Fatal(dry, err)
        }
        if len(spy.submits) != 1 || !spy.submits[0].DryRun || len(spy.lists)+len(spy.shows)+len(spy.enables) != 0 {
            t.Fatal("preview/decline performed remote work", spy)
        }
    }
}

func TestUXHSubmitEditorBackPreservesDraftAndEmptyClearsLabels(t *testing.T) {
    spy := &uxHQueueSpy{}
    // Command editor Back keeps argv; labels accept an exact JSON array with
    // spaces, then review-only exits without remote submission.
    c := uxHCallFixture(t, "zh-Hans", "2\n5\n3\n\n[\"seed 1\",\"中文标签\"]\n1\n\n", spy)
    args := []string{"printf", "space here", ""}
    c.grammar.Queue.Submit.Argv = append([]string{"--"}, args...)
    c.grammar.Queue.Submit.Name = "old name"
    c.grammar.Queue.Submit.DryRun = true
    if _, err := c.uxHSubmitWith(context.Background(), uxHInputs{editCommand: c.uxCEditCommand}); err != nil {
        t.Fatal(err)
    }
    if len(spy.submits) != 1 || !slices.Equal(spy.submits[0].Args, args) || spy.submits[0].Name != "" || !slices.Equal(spy.submits[0].Tags, []string{"seed 1", "中文标签"}) {
        t.Fatal(spy.submits)
    }
}

func TestUXHCanceledObservationStillPrintsFixedIdentities(t *testing.T) {
    c := uxHCallFixture(t, "en", "", &uxHQueueSpy{})
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    result := client.QueueSubmitResult{Stage: "enqueueing", NextAction: "inspect_original_request", Request: &queue.Request{ID: "q_saved", RunID: "r_fixed", State: "preparing"}}
    err := c.uxHShowOutcome(ctx, result, fault.New("QUEUE_ENQUEUE_UNCONFIRMED", "queue_operation_failed", map[string]any{"Code": "QUEUE_ENQUEUE_UNCONFIRMED"}))
    text := c.options.Err.(*bytes.Buffer).String()
    if err == nil || !strings.Contains(text, "q_saved") || !strings.Contains(text, "r_fixed") || c.options.Out.(*bytes.Buffer).Len() != 0 {
        t.Fatal(err, text)
    }
}
