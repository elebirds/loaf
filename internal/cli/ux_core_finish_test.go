package cli

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    execution "github.com/elebirds/loaf/internal/run"
)

func TestUXCoreMachineMissingIssuesAreLocalAndComplete(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, test := range []struct {
            args   []string
            issues []inputIssue
        }{
            {nil, []inputIssue{{"name", "missing_argument"}, {"--ssh", "missing_argument"}}},
            {[]string{"lab"}, []inputIssue{{"--ssh", "missing_argument"}}},
            {[]string{"--ssh", "private-host"}, []inputIssue{{"name", "missing_argument"}}},
        } {
            o := fixture(t)
            o.In = uxCoreNoInput{t}
            args := append([]string{"machine", "add", "--lang", language, "--json"}, test.args...)
            code, out, stderr := run(t, o, args...)
            var value envelope
            if err := json.Unmarshal([]byte(out), &value); err != nil || code != 2 || stderr != "" || value.Error == nil {
                t.Fatal(args, code, out, stderr, err)
            }
            e := value.Error
            if e.Code != "BAD_CONFIG" || e.Argument != test.issues[0].Argument || e.Reason != "missing_argument" || !reflect.DeepEqual(e.Issues, test.issues) || !reflect.DeepEqual(e.HelpCommand, []string{"loaf", "help", "machine", "add"}) || strings.Contains(out, "private-host") {
                t.Fatal("changed primary error or leaked supplied values", out)
            }
        }
    }
}

func TestUXCoreTaskSubscriptionConsumesSavedIDWithoutObservation(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o, remote, refs := psFixture(t)
        c := uxDCall(t, o, language, "")
        n := &uxINotifyFake{locations: []client.DestinationRecord{{Name: "receiver", Kind: "codex", SessionID: "exact-session"}}}
        c.app.Notify = n
        c.grammar.Target = new("replacement")
        saved, err := c.app.Projects.Inspect(context.Background(), refs[0].ID)
        if err != nil {
            t.Fatal(err)
        }
        choices := c.uxDTaskChoices(uxDTaskState{saved: saved})
        subscribe, back := uxDChoiceIndex(t, choices, "notify-run"), uxDChoiceIndex(t, choices, "back")
        // Destination, replay, review, save, result, then return to this task.
        keys := fmt.Sprintf("%d\n1\n1\n\n1\n\n%d\n", subscribe, back)
        c.input = interaction.New(interaction.Options{Interactive: true, In: strings.NewReader(keys), Err: c.output.err})
        if err := c.uxDTaskScreen(context.Background(), refs[0].ID, nil); err != nil {
            t.Fatal(err, c.options.Err)
        }
        in := n.subscription
        if in.Subject.ID != refs[0].ID || in.Subject.Kind != "run" || in.Target != nil || in.Source != "project" || !in.Replay || !reflect.DeepEqual(in.Destinations, []string{"receiver"}) || n.serveCalls != 0 || remote.calls != 0 {
            t.Fatal("subscription changed identity or observed/delivered implicitly", in, n.serveCalls, remote.calls)
        }
        // A retained local record remains subscribable; unavailable services
        // never leave an empty menu action behind.
        c.app.Notify = nil
        for _, choice := range c.uxDTaskChoices(uxDTaskState{saved: saved}) {
            if choice.Value == "notify-run" {
                t.Fatal("unavailable subscription was advertised")
            }
        }
    }
}

func TestUXCoreRunOutcomeIsOneScreenAndCanReturnToSameResult(t *testing.T) {
    o, _, _, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo ready'\n")
    c := uxCCall(t, o, "en", "", "run", "--wait", "0s")
    data, err := c.uxCRunCommand(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    v := data.(execution.View)
    saved, err := c.app.Projects.Inspect(context.Background(), v.ID)
    if err != nil {
        t.Fatal(err)
    }
    choices := c.uxCRunOutcomeChoices(v)
    task, back := uxDChoiceIndex(t, choices, "task"), uxDChoiceIndex(t, choices, "back")
    taskBack := uxDChoiceIndex(t, c.uxDTaskChoices(uxDTaskState{saved: saved}), "back")
    keys := fmt.Sprintf("%d\n%d\n%d\n%d\n%d\n", task, taskBack, task, taskBack, back)
    c.input = interaction.New(interaction.Options{Interactive: true, In: strings.NewReader(keys), Err: c.output.err})
    launches, queries := remote.launches, remote.queries
    if err := c.uxCRunOutcome(context.Background(), v, nil); err != nil {
        t.Fatal(err)
    }
    text := uxCString(c.output.err)
    if strings.Count(text, c.output.locale.Text("ux_c_next")+"\n") != 3 || strings.Contains(text, c.output.locale.Text("ux_core_result")+"\n") || remote.launches != launches || remote.queries != queries {
        t.Fatal("result forced details, failed to return, or replayed work", text, remote.launches, remote.queries)
    }
}

type uxCoreCancelOnce struct{ sent bool }

func (r *uxCoreCancelOnce) Read([]byte) (int, error) {
    if !r.sent {
        r.sent = true
        return 0, interaction.ErrCanceled
    }
    return 0, io.EOF
}

func TestUXCoreChildCancelReturnsToOverview(t *testing.T) {
    for _, route := range []string{"more", "result"} {
        o := fixture(t)
        o.Interactive = true
        keys := fmt.Sprintf("%d\n", uxCoreRootChoice(t, o, "more"))
        if route == "result" {
            if code, _, stderr := run(t, o, "machine", "add", "lab", "--ssh", "fixture-host", "--json"); code != 0 {
                t.Fatal(code, stderr)
            }
            if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("target='lab'\n[run]\ncommand='echo ready'\n"), 0600); err != nil {
                t.Fatal(err)
            }
            // This fixture has no execution service. Opening Run produces a
            // root-owned failure result, whose cancel must return to Loaf.
            keys = fmt.Sprintf("%d\n", uxCoreRootChoice(t, o, "run"))
        }
        o.In = io.MultiReader(strings.NewReader(keys), &uxCoreCancelOnce{}, strings.NewReader(fmt.Sprintf("%d\n", uxCoreRootChoice(t, o, "exit"))))
        code, out, stderr := run(t, o, "-i")
        if code != 0 || out != "" || strings.Count(stderr, "Loaf\n") != 2 {
            t.Fatal(route, code, out, stderr)
        }
    }
}
