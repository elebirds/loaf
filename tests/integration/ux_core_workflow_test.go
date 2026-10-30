package integration

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "regexp"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/charmbracelet/x/ansi"
    "github.com/creack/pty"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
    "golang.org/x/term"
)

// The plain terminal is intentional: native stdin handoffs and the real
// packaged application are exercised together without duplicating Huh's
// domain-specific rich-terminal fixtures. Choices are read from displayed
// labels, so adding another menu action cannot silently redirect the test.
type uxWorkflowTerminal struct {
    t      *testing.T
    ctx    context.Context
    master *os.File
    mu     sync.Mutex
    output bytes.Buffer
    offset int
}

func (s *uxWorkflowTerminal) snapshot() string {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.output.String()
}

func (s *uxWorkflowTerminal) wait(title, suffix string) string {
    s.t.Helper()
    ticker := time.NewTicker(5 * time.Millisecond)
    defer ticker.Stop()
    for {
        raw := s.snapshot()
        frame := strings.ReplaceAll(ansi.Strip(raw[s.offset:]), "\r", "")
        if strings.Contains(frame, title) && strings.HasSuffix(frame, suffix) {
            s.offset = len(raw)
            return frame
        }
        select {
        case <-s.ctx.Done():
            s.t.Fatalf("missing terminal prompt %q: %s", title, frame)
        case <-ticker.C:
        }
    }
}

func (s *uxWorkflowTerminal) write(answer string) {
    s.t.Helper()
    if _, err := io.WriteString(s.master, answer); err != nil {
        s.t.Fatal(err)
    }
}

var uxWorkflowChoices = regexp.MustCompile(`(?m)^\s*\*?\s*(\d+)\. ([^\n]+)$`)

func (s *uxWorkflowTerminal) choose(title string, labels ...string) string {
    s.t.Helper()
    frame := s.wait(title, "> ")
    choices := uxWorkflowChoices.FindAllStringSubmatch(frame, -1)
    for _, label := range labels {
        for _, choice := range choices {
            if strings.TrimSpace(choice[2]) == label {
                s.write(choice[1] + "\n")
                return label
            }
        }
    }
    s.t.Fatalf("no requested action %q in terminal frame: %s", labels, frame)
    return ""
}

func TestUXCoreIntegratedTerminalWorkflow(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for the packaged terminal/SSH workflow")
    }
    for _, test := range []struct {
        language   string
        rows, cols uint16
    }{{"en", 18, 80}, {"zh-Hans", 32, 120}} {
        t.Run(test.language, func(t *testing.T) {
            f := newFixture(t)
            project := t.TempDir()
            sourceFile(t, project, "loaf.toml", "[run]\nwait='5s'\ncommand='''printf 'one\\n' >> \"$HOME/ux-workflow-launches\"; printf 'ux-log-body 中文\\n'; mkdir -p outputs; printf 'collected 中文' > outputs/result.txt'''\n")
            ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
            master, terminal, err := pty.Open()
            if err != nil {
                cancel()
                t.Fatal(err)
            }
            if err := pty.Setsize(terminal, &pty.Winsize{Rows: test.rows, Cols: test.cols}); err != nil {
                t.Fatal(err)
            }
            before, err := term.GetState(int(terminal.Fd()))
            if err != nil {
                t.Fatal(err)
            }
            s := &uxWorkflowTerminal{t: t, ctx: ctx, master: master}
            drained := make(chan struct{})
            go func() {
                defer close(drained)
                data := make([]byte, 4096)
                for {
                    n, err := master.Read(data)
                    if n > 0 {
                        s.mu.Lock()
                        _, _ = s.output.Write(data[:n])
                        s.mu.Unlock()
                    }
                    if err != nil {
                        return
                    }
                }
            }()
            cmd := stopCLICommand(ctx, project, "-i", "--lang", test.language)
            cmd.Env = append(cmd.Env, "NO_COLOR=1", "TERM=xterm-256color", "HOME="+filepath.Join(project, "isolated-home"), "CODEX_HOME="+filepath.Join(project, "isolated-codex"))
            cmd.Stdin, cmd.Stdout, cmd.Stderr = terminal, terminal, terminal
            if err := cmd.Start(); err != nil {
                t.Fatal(err)
            }
            done, joined := make(chan error, 1), make(chan struct{})
            go func() { done <- cmd.Wait(); close(joined) }()
            t.Cleanup(func() {
                cancel()
                <-joined
                terminal.Close()
                master.Close()
                <-drained
                if directory := os.Getenv("LOAF_UX_CORE_WORKFLOW_CAPTURE_DIR"); directory != "" {
                    if err := os.MkdirAll(directory, 0700); err != nil {
                        t.Error(err)
                    } else if err := os.WriteFile(filepath.Join(directory, "workflow-"+test.language+".ansi"), []byte(s.snapshot()), 0600); err != nil {
                        t.Error(err)
                    }
                }
            })
            l, _ := i18n.New(test.language, nil)
            text := func(id string) string { return l.Text(id) }

            s.choose("Loaf", text("ux_core_machines"))
            s.choose(text("ux_a_hub"), text("ux_a_add"))
            s.wait(text("ux_a_name"), ": ")
            s.write("lab\n")
            s.wait(text("ux_a_connection"), ": ")
            s.write("ssh -F " + ssh.Quote(f.config) + " fixture\n")
            for s.choose(text("ux_a_review"), text("ux_a_save"), text("ux_core_next")) != text("ux_a_save") {
            }
            // Select the actual displayed alias; the prefix shows kind/current
            // selection and deliberately isn't an identity.
            frame := s.wait(text("ux_a_hub"), "> ")
            selected := false
            for _, choice := range uxWorkflowChoices.FindAllStringSubmatch(frame, -1) {
                if strings.HasSuffix(choice[2], " · lab") {
                    s.write(choice[1] + "\n")
                    selected = true
                    break
                }
            }
            if !selected {
                t.Fatal("registered machine missing", frame)
            }
            s.choose(text("ux_a_details"), text("ux_a_use"))
            s.choose(text("ux_core_result"), text("ux_core_back"))
            s.choose(text("ux_a_details"), text("ux_core_back"))
            s.choose(text("ux_a_hub"), text("ux_core_back"))
            s.choose("Loaf", text("ux_core_run"))
            s.choose(text("ux_c_run_review"), text("ux_c_submit"))
            frame = s.wait(text("ux_c_next"), "> ")
            id := regexp.MustCompile(`r_[0-9a-f]{24}`).FindString(frame)
            if id == "" || !strings.Contains(frame, text("run_status_succeeded")) {
                t.Fatal("run result lost ID or successful observation", frame)
            }
            // Select the result action from its actual frame, not a fixture
            // index, and continue through D/E/F/G in this same process.
            foundTask := false
            for _, choice := range uxWorkflowChoices.FindAllStringSubmatch(frame, -1) {
                if choice[2] == text("ux_c_open_task") {
                    s.write(choice[1] + "\n")
                    foundTask = true
                    break
                }
            }
            if !foundTask {
                t.Fatal("task navigation absent", frame)
            }
            s.choose(text("ux_d_task"), text("ux_d_logs"))
            s.choose(text("ux_e_title"), text("ux_e_recent"))
            s.wait(text("ux_e_tail"), text("ux_e_tail"))
            s.write("\n")
            s.choose(text("ux_d_task"), text("ux_d_outputs"))
            outputTitle := l.Text("ux_f_outputs", map[string]any{"ID": id})
            s.choose(outputTitle, text("ux_f_review"))
            s.choose(text("ux_f_review"), text("ux_f_download"))
            s.choose(text("ux_f_finished"), text("ux_f_clean"))
            s.choose(l.Text("ux_g_preview_title", map[string]any{"Count": 1}), text("ux_core_back"))
            s.choose(text("ux_g_title"), text("ux_g_delete"))
            confirm := l.Text("ux_g_confirm", map[string]any{"Count": 1})
            s.wait(confirm, confirm)
            s.write("y\n")
            s.choose(l.Text("ux_g_result_title", map[string]any{"Count": 1}), text("ux_core_back"))
            s.choose(text("ux_f_finished"), text("ux_f_back"))
            s.choose(text("ux_d_task"), text("ux_core_back"))
            frame = s.wait(text("ux_c_next"), "> ")
            if !strings.Contains(frame, text("clean_status_cleaned")) || strings.Contains(frame, text("ux_c_open_logs")) || strings.Contains(frame, ". "+text("ux_d_outputs")+"\n") {
                t.Fatal("result offered stale payload actions after cleanup", frame)
            }
            left := false
            for _, choice := range uxWorkflowChoices.FindAllStringSubmatch(frame, -1) {
                if choice[2] == text("ux_core_back") {
                    s.write(choice[1] + "\n")
                    left = true
                    break
                }
            }
            if !left {
                t.Fatal("cleaned result cannot return to overview", frame)
            }
            s.choose("Loaf", text("ux_core_exit"))
            if err := <-done; err != nil {
                t.Fatal("workflow did not exit cleanly", err, s.snapshot())
            }
            after, err := term.GetState(int(terminal.Fd()))
            if err != nil || !reflect.DeepEqual(before, after) {
                t.Fatal("workflow changed terminal configuration", err)
            }
            if !strings.Contains(s.snapshot(), "ux-log-body 中文") {
                t.Fatal("native logs were not consumed")
            }
            data, err := os.ReadFile(filepath.Join(project, "loaf-results", id, "outputs", "result.txt"))
            if err != nil || string(data) != "collected 中文" {
                t.Fatal("downloaded output was not preserved after cleanup", string(data), err)
            }
            saved := runCLI(t, project, "status", id, "--json")
            if saved.Target != "lab" || saved.Status != "succeeded" || saved.CleanedAt == nil {
                t.Fatal("cleanup lost frozen facts", saved)
            }
            if got := f.shell(`wc -l < "$HOME/ux-workflow-launches"`); strings.TrimSpace(got) != "1" {
                t.Fatal("navigation replayed submission", got)
            }
            f.shell(`test ! -e "$1/runs/$2"`, saved.RemoteDir, id)
            t.Log(fmt.Sprintf("packaged %s terminal %dx%d: registration, one submission, native logs, pull, cleanup, retained facts", test.language, test.cols, test.rows))
        })
    }
}
