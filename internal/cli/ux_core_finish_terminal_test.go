package cli

import (
    "context"
    "fmt"
    "io"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "golang.org/x/term"
)

func TestUXCoreRichRunOutcomeHasImmediateActions(t *testing.T) {
    t.Setenv("TERM", "xterm-256color")
    t.Setenv("COLORTERM", "truecolor")
    t.Setenv("NO_COLOR", "")
    for _, language := range []string{"en", "zh-Hans"} {
        for _, uncertain := range []bool{false, true} {
            t.Run(fmt.Sprintf("%s/uncertain=%t", language, uncertain), func(t *testing.T) {
                o, _, _, remote := uxCFixture(t, "target='lab'\n[run]\ncommand='echo ready'\n")
                if uncertain {
                    remote.launchErr = fault.New("SSH_FAILED", "ssh_failed", nil)
                }
                c := uxCCall(t, o, language, "", "run", "--wait", "0s")
                // Production has output browsing too. Include that fifth
                // action in the short-terminal render, without querying it.
                c.app.Projects.Outputs, c.app.Projects.Puller = &uxFBrowser{}, &fakePuller{}
                data, cause := c.uxCRunCommand(context.Background())
                v := data.(execution.View)
                master, terminal, err := pty.Open()
                if err != nil {
                    t.Fatal(err)
                }
                drained := uxCoreDrainTerminal(master, false)
                t.Cleanup(func() { terminal.Close(); master.Close(); <-drained })
                if err := pty.Setsize(terminal, &pty.Winsize{Rows: 18, Cols: 80}); err != nil {
                    t.Fatal(err)
                }
                before, err := term.GetState(int(terminal.Fd()))
                if err != nil {
                    t.Fatal(err)
                }
                output := &wizardTerminal{File: terminal}
                c.options.In, c.options.Err, c.output.err = terminal, output, output
                l := c.output.locale
                c.input = interaction.New(interaction.Options{In: terminal, Err: output, Interactive: true, Rich: true, Compact: true, PageHelp: l.Text("ux_core_form_help"), SelectHelp: l.Text("ux_core_select_help")})
                ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
                done, joined := make(chan error, 1), make(chan struct{})
                go func() { defer close(joined); done <- c.uxCRunOutcome(ctx, v, cause) }()
                t.Cleanup(func() { cancel(); <-joined })
                uxCoreWaitFrame(t, ctx, output, 0, l.Text("ux_c_open_task"), l.Text("ux_core_select_help"))
                uxCoreWaitTheme(t, ctx, output, 0, false)
                uxCoreCapture(t, output, fmt.Sprintf("outcome-%s-uncertain-%t-80x18", language, uncertain))
                if _, err := io.WriteString(master, strings.Repeat("\x1b[B", len(c.uxCRunOutcomeChoices(v))-1)+"\r"); err != nil {
                    t.Fatal(err)
                }
                if err := <-done; err != nil {
                    t.Fatal(err)
                }
                after, err := term.GetState(int(terminal.Fd()))
                if err != nil || !reflect.DeepEqual(before, after) {
                    t.Fatal("outcome did not restore terminal", err)
                }
            })
        }
    }
}
