package integration

import (
    "bytes"
    "context"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strconv"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
)

func operationCLI(t *testing.T, project string, args ...string) *exec.Cmd {
    t.Helper()
    cmd := exec.Command(os.Getenv("LOAF_BIN"), args...)
    cmd.Dir = project
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(project, "config"), "XDG_STATE_HOME="+filepath.Join(project, "state"), "LOAF_LANG=en")
    return cmd
}

func TestForegroundExecWorkflow(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    project := t.TempDir()
    if code, out := cli(t, project, "add", "machine", "--ssh", "ssh -F "+ssh.Quote(f.config)+" fixture", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    // These operations need neither a selected project nor an uploaded runner.
    var stdout, stderr bytes.Buffer
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    args := []string{"/bin/sh", "-c", `printf '%s\000' "$PWD" "$@"; cat; printf diagnostic >&2`, "argv-zero", "space 中文", "$(touch injected)", "'\"", "", "--json"}
    result, err := f.client.Exec(ctx, f.target, ssh.ExecInput{Args: args, Batch: true}, strings.NewReader("stdin-bytes"), &stdout, &stderr)
    want := "/home/researcher\x00" + strings.Join(args[4:], "\x00") + "\x00stdin-bytes"
    if err != nil || result.Status != "succeeded" || stdout.String() != want || stderr.String() != "diagnostic" {
        t.Fatal(result, err, stdout.String(), stderr.String())
    }
    f.shell(`test ! -e "$HOME/injected"; test ! -e "$HOME/.local/share/loaf"; mkdir -p "$HOME/work space 中文"`)
    stdout.Reset()
    stderr.Reset()
    result, err = f.client.Exec(ctx, f.target, ssh.ExecInput{Args: []string{"pwd"}, CWD: "~/work space 中文", Batch: true}, nil, &stdout, &stderr)
    if err != nil || stdout.String() != "/home/researcher/work space 中文\n" {
        t.Fatal(result, err, stdout.String(), stderr.String())
    }
    for _, code := range []int{7, 255} {
        result, err = f.client.Exec(ctx, f.target, ssh.ExecInput{Args: []string{"/bin/sh", "-c", "exit " + strconv.Itoa(code)}, Batch: true}, nil, io.Discard, io.Discard)
        var problem *fault.Error
        want := "EXEC_FAILED"
        if code == 255 {
            want = "EXEC_UNCONFIRMED"
        }
        if !errors.As(err, &problem) || problem.Code != want || code == 7 && (result.ExitCode == nil || *result.ExitCode != 7) || code == 255 && result.ExitCode != nil {
            t.Fatal(result, err)
        }
    }
    // Decode the first flushed chunk before allowing the remote command to finish.
    cmd := operationCLI(t, project, "exec", "--target", "machine", "--json", "--", "/bin/sh", "-c", `printf 'early 中文\n'; printf 'warning\n' >&2; while [ ! -f "$HOME/release-exec" ]; do sleep .02; done; printf '\377\376'; exit 7`)
    pipe, err := cmd.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    stderr.Reset()
    cmd.Stderr = &stderr
    if err := cmd.Start(); err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() {
        if cmd.ProcessState == nil {
            _ = cmd.Process.Kill()
            _ = cmd.Wait()
        }
    })
    decoder := json.NewDecoder(pipe)
    received := map[string][]byte{}
    released, ended := false, false
    for {
        var event struct {
            OK   bool `json:"ok"`
            Data struct {
                Type, Stream, Encoding, Data, Status string
                Offset                               int64
                ExitCode                             *int `json:"exit_code"`
            }   `json:"data"`
            Error *struct{ Code string } `json:"error"`
        }
        err := decoder.Decode(&event)
        if errors.Is(err, io.EOF) {
            break
        }
        if err != nil {
            t.Fatal(err)
        }
        if event.Data.Type == "chunk" {
            data := []byte(event.Data.Data)
            if event.Data.Encoding == "base64" {
                data, err = base64.StdEncoding.DecodeString(event.Data.Data)
                if err != nil {
                    t.Fatal(err)
                }
            } else if event.Data.Encoding != "utf-8" {
                t.Fatal(event)
            }
            if event.Data.Offset != int64(len(received[event.Data.Stream])) || len(data) > 32<<10 {
                t.Fatal(event)
            }
            received[event.Data.Stream] = append(received[event.Data.Stream], data...)
            if !released && bytes.Contains(received["stdout"], []byte("early 中文\n")) {
                f.shell(`touch "$HOME/release-exec"`)
                released = true
            }
        } else {
            ended = event.Data.Type == "error" && event.Data.Status == "failed" && event.Data.ExitCode != nil && *event.Data.ExitCode == 7 && event.Error != nil && event.Error.Code == "EXEC_FAILED"
        }
    }
    err = cmd.Wait()
    var exit *exec.ExitError
    if !errors.As(err, &exit) || exit.ExitCode() != 1 || stderr.Len() != 0 || !released || !ended || !bytes.Equal(received["stdout"], append([]byte("early 中文\n"), 0xff, 0xfe)) || string(received["stderr"]) != "warning\n" {
        t.Fatal(err, stderr.String(), released, ended, received)
    }
    // Cancellation closes observation, never retries the command or calls stop.
    operationCtx, operationCancel := context.WithCancel(ctx)
    ready := &operationReady{signal: make(chan struct{})}
    done := make(chan error, 1)
    go func() {
        _, err := f.client.Exec(operationCtx, f.target, ssh.ExecInput{Args: []string{"/bin/sh", "-c", `printf x >> "$HOME/exec-count"; printf ready; exec sleep 120`}, Batch: true}, nil, ready, io.Discard)
        done <- err
    }()
    select {
    case <-ready.signal:
        operationCancel()
    case <-time.After(10 * time.Second):
        operationCancel()
        t.Fatal("no live command output")
    }
    select {
    case err := <-done:
        var problem *fault.Error
        if !errors.As(err, &problem) || problem.Code != "EXEC_UNCONFIRMED" {
            t.Fatal(err)
        }
    case <-time.After(5 * time.Second):
        t.Fatal("canceled exec did not close SSH")
    }
    if count := f.shell(`cat "$HOME/exec-count"`); count != "x" {
        t.Fatal("exec was replayed", count)
    }
    f.shell(`test ! -e "$HOME/.local/share/loaf"`)
    if _, err := os.Stat(local.New(filepath.Join(project, "state", "loaf")).Path); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("machine operations created run/project state", err)
    }
}

type operationReady struct {
    once   sync.Once
    signal chan struct{}
}

func (w *operationReady) Write(data []byte) (int, error) {
    w.once.Do(func() { close(w.signal) })
    return len(data), nil
}

func TestForwardIsolationAndInteractiveSSH(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    f.shell(`nc -lk -s 127.0.0.1 -p 18080 -e /bin/cat </dev/null >/dev/null 2>&1 & echo $! > "$HOME/service.pid"; sleep 300 </dev/null >/dev/null 2>&1 & echo $! > "$HOME/sentinel.pid"`)
    socketDir, err := os.MkdirTemp("/tmp", "loaf-master-")
    if err != nil {
        t.Fatal(err)
    }
    defer os.RemoveAll(socketDir)
    socket := filepath.Join(socketDir, "master")
    originalPort := operationPort(t)
    content, err := os.ReadFile(f.config)
    if err != nil {
        t.Fatal(err)
    }
    content = []byte(strings.Replace(string(content), "ControlMaster no", "ControlMaster auto\n    ControlPersist 600\n    ForkAfterAuthentication yes\n    ControlPath "+socket, 1))
    if err := os.WriteFile(f.config, content, 0600); err != nil {
        t.Fatal(err)
    }
    command(t, "ssh", "-F", f.config, "-M", "-f", "-N", "-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:18080", originalPort), "fixture")
    defer func() {
        out, err := exec.Command("ssh", "-F", f.config, "-o", "ForkAfterAuthentication=no", "-O", "exit", "fixture").CombinedOutput()
        if err != nil {
            t.Errorf("close fixture master: %v %s", err, out)
        }
    }()
    wait(t, 5*time.Second, func() bool { return operationEcho(originalPort) })
    local := operationPort(t)
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    var diagnostic bytes.Buffer
    go func() { done <- f.client.Forward(ctx, f.target, local, 18080, true, &diagnostic) }()
    defer cancel()
    wait(t, 10*time.Second, func() bool { return operationEcho(local) })
    cancel()
    select {
    case err := <-done:
        if err != nil {
            t.Fatal(err, diagnostic.String())
        }
    case <-time.After(5 * time.Second):
        t.Fatal("forward cancellation left a process running")
    }
    if operationEcho(local) {
        t.Fatal("canceled forwarding remains active")
    }
    if !operationEcho(originalPort) {
        t.Fatal("canceling Loaf forwarding removed an unrelated master forwarding")
    }
    command(t, "ssh", "-F", f.config, "-o", "ForkAfterAuthentication=no", "-O", "check", "fixture")
    f.shell(`kill -0 "$(cat "$HOME/sentinel.pid")"; kill -0 "$(cat "$HOME/service.pid")"`)
    occupied, err := net.Listen("tcp4", "127.0.0.1:0")
    if err != nil {
        t.Fatal(err)
    }
    defer occupied.Close()
    port := occupied.Addr().(*net.TCPAddr).Port
    failureCtx, failureCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer failureCancel()
    diagnostic.Reset()
    err = f.client.Forward(failureCtx, f.target, port, 18080, true, &diagnostic)
    var problem *fault.Error
    if !errors.As(err, &problem) || problem.Code != "SSH_FAILED" || !strings.Contains(diagnostic.String(), "forwarding") {
        t.Fatal(err, diagnostic.String())
    }
    // An existing authenticated master must not hide failed authentication on
    // the forwarding's independent connection.
    denied := f.target
    denied.Args = append(append([]string{}, denied.Args...), "-l", "missing-user")
    diagnostic.Reset()
    err = f.client.Forward(failureCtx, denied, operationPort(t), 18080, true, &diagnostic)
    if !errors.As(err, &problem) || problem.Code != "SSH_FAILED" || !strings.Contains(diagnostic.String(), "Permission denied") {
        t.Fatal("forward reused another authenticated connection", err, diagnostic.String())
    }
    // A real controlling terminal lets native SSH own PTY setup and raw modes.
    script, err := exec.LookPath("script")
    if err != nil {
        t.Fatal("the native script utility is required for the PTY integration case", err)
    }
    project := t.TempDir()
    if code, out := cli(t, project, "add", "machine", "--ssh", "ssh -F "+ssh.Quote(f.config)+" fixture", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    client := operationCLI(t, project, "ssh", "--target", "machine")
    var tty *exec.Cmd
    if runtime.GOOS == "darwin" {
        tty = exec.Command(script, append([]string{"-q", "/dev/null", client.Path}, client.Args[1:]...)...)
    } else {
        words := append([]string{client.Path}, client.Args[1:]...)
        for i := range words {
            words[i] = ssh.Quote(words[i])
        }
        tty = exec.Command(script, "-q", "-e", "-c", strings.Join(words, " "), "/dev/null")
    }
    tty.Dir, tty.Env = client.Dir, client.Env
    tty.Stdin = strings.NewReader("printf 'interactive-%s\\n' \"$(id -un)\"\nexit\n")
    output, err := tty.CombinedOutput()
    if err != nil || !strings.Contains(string(output), "interactive-researcher") {
        t.Fatal(err, string(output))
    }
    if !operationEcho(originalPort) {
        t.Fatal("interactive SSH closed an existing forwarding")
    }
}

func operationPort(t *testing.T) int {
    t.Helper()
    listener, err := net.Listen("tcp4", "127.0.0.1:0")
    if err != nil {
        t.Fatal(err)
    }
    port := listener.Addr().(*net.TCPAddr).Port
    if err := listener.Close(); err != nil {
        t.Fatal(err)
    }
    return port
}

func operationEcho(port int) bool {
    connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
    if err != nil {
        return false
    }
    defer connection.Close()
    _ = connection.SetDeadline(time.Now().Add(300 * time.Millisecond))
    const text = "forwarded-service\n"
    if _, err := io.WriteString(connection, text); err != nil {
        return false
    }
    body := make([]byte, len(text))
    _, err = io.ReadFull(connection, body)
    return err == nil && string(body) == text
}
