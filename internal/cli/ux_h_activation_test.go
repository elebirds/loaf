package cli

import (
    "context"
    "io"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/queue"
)

func TestUXHIntegratedBareQueueAndExplicitList(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        spy := &uxHQueueSpy{result: queue.Result{Status: &queue.Status{TargetID: "shared", Slots: 1, CoordinatorRunning: true}}}
        spy.changed = spy.result
        spy.submitResult.Stage = "admitted"
        spy.submitResult.Request = &queue.Request{ID: "q_1", RunID: "r_1", State: "queued"}
        o := fixture(t)
        o.Queue, o.Interactive, o.In = spy, true, strings.NewReader("5\n")
        code, out, stderr := run(t, o, "-i", "--lang", lang, "queue")
        if code != 0 || out != "" || stderr == "" || len(spy.lists) != 1 {
            t.Fatal(code, out, stderr, spy.lists)
        }
        for _, args := range [][]string{{"queue", "list"}, {"queue", "list", "--limit", "100"}, {"queue", "--trace=false"}, {"queue", "--json"}, {"queue", "list", "--json"}, {"queue", "--help"}, {"help", "queue"}, {"queue", "enable", "--slots", "1"}, {"queue", "submit", "--", "printf", "literal"}} {
            o.In = uxCoreNoInput{t}
            code, out, stderr = run(t, o, args...)
            if code != 0 || out == "" || stderr != "" {
                t.Fatal(args, code, out, stderr)
            }
        }
        if len(spy.enables) != 1 || len(spy.submits) != 1 {
            t.Fatal("direct call or browsing replayed a mutation", spy)
        }
    }
}

func TestUXHIntegratedRunRoutesRetainFrozenIdentity(t *testing.T) {
    c, _, _, refs := uxFCall(t, "en", "")
    c.grammar.Target = new("replacement")
    row := queue.Request{ID: "q_saved", RunID: refs[0].ID, State: "dispatch_unconfirmed"}
    routes := c.uxHRunRoutes(row, true)
    if len(routes) != 3 || routes[0].Kind != "task" || routes[1].Kind != "logs" || routes[2].Kind != "outputs" {
        t.Fatal("integrated run actions missing", routes)
    }
    for _, route := range routes {
        if route.ID != row.RunID || route.Target != nil || len(route.IDs) != 0 || route.validate() != nil {
            t.Fatal("historical route inherited current selection", route)
        }
    }
    if len(c.uxHRunRoutes(row, false)) != 0 {
        t.Fatal("foreign request acquired local actions")
    }
}

func TestUXHIntegratedNavigationBindsTemporaryTarget(t *testing.T) {
    spy := &uxHQueueSpy{result: queue.Result{Status: &queue.Status{TargetID: "shared", Slots: 1, CoordinatorRunning: true}}}
    c := uxHCallFixture(t, "en", "5\n", spy)
    if !c.uxRouteAvailable("queue") || !c.uxRouteAvailable("queue-submit") {
        t.Fatal("queue routes not connected")
    }
    _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "queue", Target: new("temporary")})
    if err != nil || len(spy.lists) != 1 || *spy.lists[0].Target != "temporary" || *c.grammar.Target != "lab" {
        t.Fatal(err, spy.lists)
    }
    c = uxHCallFixture(t, "en", "6\n", spy)
    if _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "queue-submit", Target: new("temporary")}); err != nil || len(spy.submits) != 0 {
        t.Fatal(err, spy.submits)
    }
}

func TestUXHIntegratedPlainTerminalModes(t *testing.T) {
    for _, mode := range []string{"NO_COLOR", "TERM=dumb"} {
        t.Run(mode, func(t *testing.T) {
            spy := &uxHQueueSpy{result: queue.Result{Status: &queue.Status{TargetID: "shared", Slots: 1, CoordinatorRunning: true}}}
            c, master, _, output := uxHTerminal(t, "en", 80, 18, spy)
            c.options.App, c.options.Interactive, c.options.TerminalError = c.app, true, true
            c.options.Env = map[string]string{"TERM": "xterm-256color", "LANG": "en"}
            if mode == "NO_COLOR" {
                c.options.Env["NO_COLOR"] = "1"
            } else {
                c.options.Env["TERM"] = "dumb"
            }
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            done, joined := make(chan int, 1), make(chan struct{})
            go func() { defer close(joined); done <- Execute(ctx, []string{"-i", "queue"}, c.options) }()
            t.Cleanup(func() { cancel(); <-joined })
            uxHWaitFrame(t, ctx, output, 0, "> ")
            uxHCapture(t, output, "hub-plain-"+mode+"-80x18")
            _, _ = io.WriteString(master, "5\n")
            if code := <-done; code != 0 || len(spy.lists) != 1 {
                t.Fatal(code, spy.lists)
            }
            if strings.Contains(output.raw(), "\x1b[") {
                t.Fatal("plain terminal rendered control sequences", output.raw())
            }
        })
    }
}
