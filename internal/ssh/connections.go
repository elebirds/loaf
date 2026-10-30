//go:build !windows

package ssh

import (
    "context"
    "encoding/json"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync"
    "syscall"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// Connections owns cloud SSH masters for one CLI invocation. Static targets
// retain their native SSH configuration, including user-owned multiplexing.
// Each frozen binding and argument set gets its own private connection.
type Connections struct {
    mu      sync.Mutex
    entries map[string]*connectionEntry
    closed  bool
}

type connectionEntry struct {
    gate    chan struct{}
    master  *commandConnection
    started bool
}

type commandConnection struct {
    session  connectionSession
    process  *exec.Cmd
    lifeline io.Closer
    dir      string
    done     chan struct{}
    err      error
    once     sync.Once
}

// NewConnections starts no processes and reads no credentials. The caller must
// close it before exiting, including after cancellation or a failed command.
func NewConnections() *Connections {
    return &Connections{entries: map[string]*connectionEntry{}}
}

// Close releases only masters created by this invocation, never a user's master.
func (s *Connections) Close() {
    s.mu.Lock()
    s.closed = true
    entries := make([]*connectionEntry, 0, len(s.entries))
    for _, entry := range s.entries {
        entries = append(entries, entry)
    }
    s.mu.Unlock()
    for _, entry := range entries {
        entry.gate <- struct{}{}
        if entry.master != nil {
            entry.master.close()
            entry.master = nil
        }
        <-entry.gate
    }
}

func (s *Connections) prepare(ctx context.Context, c *Client, target config.Target, batch bool) (connectionSession, error) {
    key, err := json.Marshal(struct {
        Target config.Target
        Batch  bool
    }{target, batch})
    if err != nil {
        return connectionSession{}, err
    }
    s.mu.Lock()
    if s.closed {
        s.mu.Unlock()
        return connectionSession{}, fault.At(fault.New("SSH_FAILED", "ssh_failed", nil), "ssh_connect")
    }
    entry := s.entries[string(key)]
    if entry == nil {
        entry = &connectionEntry{gate: make(chan struct{}, 1)}
        s.entries[string(key)] = entry
    }
    s.mu.Unlock()
    select {
    case <-ctx.Done():
        return connectionSession{}, fault.At(fault.Wrap("SSH_FAILED", "ssh_failed", ctx.Err()), "ssh_connect")
    case entry.gate <- struct{}{}:
    }
    defer func() { <-entry.gate }()
    if err := ctx.Err(); err != nil {
        return connectionSession{}, fault.At(fault.Wrap("SSH_FAILED", "ssh_failed", err), "ssh_connect")
    }
    s.mu.Lock()
    closed := s.closed
    s.mu.Unlock()
    if closed {
        return connectionSession{}, fault.At(fault.New("SSH_FAILED", "ssh_failed", nil), "ssh_connect")
    }
    if entry.master != nil {
        select {
        case <-entry.master.done:
            entry.master.close()
            entry.master = nil
        default:
        }
    }
    if entry.master == nil {
        entry.master, err = c.startConnection(ctx, target, batch)
        if err != nil {
            return connectionSession{}, err
        }
        c.Trace.established(entry.started)
        entry.started = true
    }
    return entry.master.session, nil
}

func (c *Client) startConnection(ctx context.Context, target config.Target, batch bool) (result *commandConnection, err error) {
    finish := c.Trace.stage("ssh_connect")
    defer func() { finish(err) }()
    direct, err := c.prepareDirect(ctx, target, batch)
    if err != nil {
        return nil, err
    }
    defer direct.close()
    dir, err := os.MkdirTemp("/tmp", "loaf-ssh-")
    if err != nil {
        return nil, fault.At(fault.Wrap("SSH_FAILED", "ssh_failed", err), "ssh_connect")
    }
    socket := filepath.Join(dir, "control")
    input, lifeline, err := os.Pipe()
    if err != nil {
        os.RemoveAll(dir)
        return nil, fault.At(fault.Wrap("SSH_FAILED", "ssh_failed", err), "ssh_connect")
    }
    masterTarget := direct.target
    masterTarget.Args = append([]string{
        "-o", "ControlMaster=yes", "-o", "ControlPath=" + socket, "-o", "ControlPersist=no",
        "-o", "ForkAfterAuthentication=no", "-o", "ClearAllForwardings=yes",
        "-o", "RemoteCommand=none", "-o", "SessionType=default",
    }, masterTarget.Args...)
    // The empty pipe is a lifetime tether, not a data relay. If the CLI is
    // killed, EOF ends this shell and its non-persistent master automatically.
    cmd := exec.Command(c.Binary, arguments(masterTarget, "exec cat >/dev/null", direct.batch)...)
    diagnostic := &lockedDiagnostic{}
    cmd.Stdin, cmd.Stdout, cmd.Stderr = input, io.Discard, diagnostic
    cmd.Env = direct.env
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    cmd.WaitDelay = time.Second
    master := &commandConnection{process: cmd, lifeline: lifeline, dir: dir, done: make(chan struct{})}
    if err := cmd.Start(); err != nil {
        input.Close()
        lifeline.Close()
        os.RemoveAll(dir)
        return nil, fault.At(fault.Wrap("SSH_FAILED", "ssh_failed", err), "ssh_connect")
    }
    input.Close()
    go func() {
        master.err = cmd.Wait()
        close(master.done)
    }()
    ticker := time.NewTicker(25 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            master.close()
            return nil, fault.At(fault.Wrap("SSH_FAILED", "ssh_failed", ctx.Err()), "ssh_connect")
        case <-master.done:
            master.close()
            return nil, connectionFailure(master.err, diagnostic.String())
        case <-ticker.C:
            check := exec.CommandContext(ctx, c.Binary, "-S", socket, "-O", "check", "--", direct.target.Destination)
            check.Env = direct.env
            if check.Run() != nil {
                continue
            }
            shared := direct
            shared.close = func() {} // Connections, not a single request, owns it.
            shared.masterDone = master.done
            shared.batch = true
            shared.env = withoutEnv(withoutEnv(withoutEnv(direct.env, "SSH_ASKPASS"), "SSH_ASKPASS_REQUIRE"), "LOAF_ASKPASS_SOCKET")
            shared.env = append(shared.env, "SSH_ASKPASS_REQUIRE=never")
            // Native SSH normally falls back to a new connection if a mux
            // socket dies. Refuse that fallback: a mutation must not silently
            // change transport or authenticate again after ambiguous delivery.
            shared.target.Args = append([]string{
                "-o", "ControlMaster=no", "-o", "ControlPath=" + socket, "-o", "ControlPersist=no",
                "-o", "ProxyCommand=false", "-o", "ProxyJump=none", "-o", "BatchMode=yes",
                "-o", "ForkAfterAuthentication=no", "-o", "ClearAllForwardings=yes",
                "-o", "RemoteCommand=none", "-o", "SessionType=default",
            }, muxArguments(direct.target.Args)...)
            master.session = shared
            return master, nil
        }
    }
}

// The master already owns the jump chain. Keeping a second explicit -J would
// conflict with the mux client's ProxyJump=none (and must never open a new hop).
func muxArguments(args []string) []string {
    result := make([]string, 0, len(args))
    for i := 0; i < len(args); i++ {
        arg := args[i]
        if strings.HasPrefix(arg, "-J") {
            if arg == "-J" {
                i++
            }
            continue
        }
        if strings.HasPrefix(arg, "-o") {
            value := strings.TrimPrefix(arg, "-o")
            if arg == "-o" && i+1 < len(args) {
                value = args[i+1]
            }
            fields := strings.FieldsFunc(value, func(r rune) bool { return r == '=' || r == ' ' || r == '\t' })
            if len(fields) > 0 && strings.EqualFold(fields[0], "ProxyJump") {
                if arg == "-o" {
                    i++
                }
                continue
            }
        }
        result = append(result, arg)
    }
    return result
}

func connectionFailure(cause error, detail string) error {
    f := fault.Wrap("SSH_FAILED", "ssh_failed", cause)
    f.Hint = "cloud_ssh_hint"
    f.Params = map[string]any{"Detail": detail}
    return fault.At(f, "ssh_connect")
}

func (m *commandConnection) close() {
    m.once.Do(func() {
        m.lifeline.Close()
        select {
        case <-m.done:
        case <-time.After(time.Second):
            _ = syscall.Kill(-m.process.Process.Pid, syscall.SIGTERM)
            select {
            case <-m.done:
            case <-time.After(time.Second):
                _ = syscall.Kill(-m.process.Process.Pid, syscall.SIGKILL)
                <-m.done
            }
        }
        _ = os.RemoveAll(m.dir)
    })
}

type lockedDiagnostic struct {
    mu   sync.Mutex
    tail diagnosticTail
}

func (d *lockedDiagnostic) Write(p []byte) (int, error) {
    d.mu.Lock()
    defer d.mu.Unlock()
    return d.tail.Write(p)
}

func (d *lockedDiagnostic) String() string {
    d.mu.Lock()
    defer d.mu.Unlock()
    return d.tail.String()
}
