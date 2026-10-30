package integration

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/creack/pty"
)

// People and Agents use the same finite entry points. No input is sent, even
// with -i: that flag permits operation-local input, not a domain dashboard.
func TestPackagedTerminalQueriesStayFinite(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for packaged PTY entry checks")
    }
    for _, test := range []struct {
        name string
        args []string
        code int
        json bool
    }{
        {"help", nil, 0, false},
        {"interactive-help", []string{"-i"}, 0, false},
        {"cloud-help", []string{"cloud"}, 0, false},
        {"interactive-cloud-help", []string{"cloud", "-i"}, 0, false},
        {"machines", []string{"machine"}, 0, false},
        {"interactive-machines", []string{"machine", "-i"}, 0, false},
        {"interactive-local-status", []string{"status", "--local", "-i"}, 0, false},
        {"interactive-deliveries", []string{"notify", "-i"}, 0, false},
        {"interactive-events", []string{"events", "-i"}, 0, false},
        {"missing-machine", []string{"machine", "add"}, 2, false},
        {"missing-resource", []string{"cloud", "acquire"}, 2, false},
        {"explicit-false", []string{"--interactive=false"}, 0, false},
        {"json", []string{"--json"}, 0, true},
        {"conflict", []string{"-i", "--json"}, 2, true},
    } {
        t.Run(test.name, func(t *testing.T) {
            project := t.TempDir()
            ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
            defer cancel()
            cmd := stopCLICommand(ctx, project, test.args...)
            cmd.Env = append(cmd.Env, "TERM=xterm-256color", "NO_COLOR=1", "HOME="+filepath.Join(project, "home"), "CODEX_HOME="+filepath.Join(project, "codex"))
            var diagnostic bytes.Buffer
            cmd.Stderr = &diagnostic
            master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 18, Cols: 80})
            if err != nil {
                t.Fatal(err)
            }
            defer master.Close()
            output := make(chan []byte, 1)
            go func() { data, _ := io.ReadAll(master); output <- data }()
            err = cmd.Wait()
            if ctx.Err() != nil {
                t.Fatal("ordinary PTY call waited for input", test.args, ctx.Err())
            }
            var exit *exec.ExitError
            if err != nil && !errors.As(err, &exit) {
                t.Fatal(err)
            }
            if got := cmd.ProcessState.ExitCode(); got != test.code {
                t.Fatal(test.args, got, diagnostic.String())
            }
            var data []byte
            select {
            case data = <-output:
            case <-ctx.Done():
                master.Close()
                <-output
                t.Fatal("packaged call did not close its output")
            }
            if test.json {
                if !json.Valid(data) || diagnostic.Len() != 0 {
                    t.Fatal("JSON mixed with prompts/progress", string(data), diagnostic.String())
                }
            } else if test.code == 0 && len(data) == 0 || test.code != 0 && diagnostic.Len() == 0 {
                t.Fatal("finite call lost its result", test.args, string(data), diagnostic.String())
            }
            if strings.Contains(string(data)+diagnostic.String(), "\x1b]11;?") {
                t.Fatal("ordinary call started an interactive terminal renderer")
            }
        })
    }
}
