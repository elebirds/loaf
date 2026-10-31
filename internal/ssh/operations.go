package ssh

import (
    "context"
    "errors"
    "io"
    "os"
    "os/exec"
    "strconv"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// ExecInput describes a foreground machine operation, not a durable Loaf run.
type ExecInput struct {
    Args  []string
    CWD   string
    Batch bool
    // TrustOnly is a native, human-owned host-key handshake, not a shell.
    // It disables authentication and only permits the fixed no-op command.
    TrustOnly bool
}

// ExecResult reports only results confirmed by OpenSSH's exit status.
type ExecResult struct {
    Status   string `json:"status"`
    ExitCode *int   `json:"exit_code,omitempty"`
}

// Exec streams a single command without synchronization, setup, or resubmission.
// SSH uses 255 for transport errors, so that value cannot confirm a command result.
func (c *Client) Exec(ctx context.Context, target config.Target, in ExecInput, stdin io.Reader, stdout, stderr io.Writer) (ExecResult, error) {
    result := ExecResult{Status: "not_started"}
    if in.TrustOnly && (len(in.Args) != 1 || in.Args[0] != "/bin/true" || in.CWD != "" || in.Batch) {
        return result, fault.New("BAD_CONFIG", "exec_command", nil)
    }
    session, err := c.prepareDirect(ctx, target, in.Batch)
    if err != nil {
        return result, err
    }
    defer session.close()
    target, in.Batch = session.target, session.batch
    command, err := execCommand(in.Args, in.CWD)
    if err != nil {
        return result, err
    }
    args := []string{"-o", "ForkAfterAuthentication=no", "-o", "RemoteCommand=none", "-o", "SessionType=default"}
    if in.TrustOnly {
        args = append(args, "-o", "StrictHostKeyChecking=ask", "-o", "BatchMode=no", "-o", "PreferredAuthentications=none", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "PubkeyAuthentication=no", "-o", "GSSAPIAuthentication=no", "-o", "HostbasedAuthentication=no")
        if session.env == nil {
            session.env = os.Environ()
        }
        session.env = append(withoutEnv(session.env, "SSH_ASKPASS_REQUIRE"), "SSH_ASKPASS_REQUIRE=never")
    }
    args = append(args, arguments(target, command, in.Batch)...)
    cmd := foregroundProcess(ctx, c.Binary, args...)
    cmd.Env = session.env
    diagnostic := &diagnosticWriter{out: stderr}
    cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, diagnostic
    if err := cmd.Start(); err != nil {
        return result, fault.Wrap("SSH_FAILED", "ssh_failed", err)
    }
    err = cmd.Wait()
    if err == nil {
        code := 0
        return ExecResult{Status: "succeeded", ExitCode: &code}, nil
    }
    var exit *exec.ExitError
    if ctx.Err() == nil && errors.As(err, &exit) && exit.ExitCode() >= 0 && exit.ExitCode() < 255 {
        code := exit.ExitCode()
        result = ExecResult{Status: "failed", ExitCode: &code}
        return result, fault.New("EXEC_FAILED", "exec_failed", map[string]any{"Value": code})
    }
    result.Status = "unconfirmed"
    if ctx.Err() != nil {
        err = errors.Join(err, ctx.Err())
    }
    f := fault.Wrap("EXEC_UNCONFIRMED", "exec_unconfirmed", classifyConnection(err, diagnostic.data))
    f.Hint = "exec_unconfirmed_hint"
    return result, f
}

// Interactive opens the SSH user's native interactive shell. The caller supplies
// real terminal descriptors so OpenSSH owns terminal modes and window resizing.
func (c *Client) Interactive(ctx context.Context, target config.Target, stdin io.Reader, stdout, stderr io.Writer) error {
    cloudTarget := target.Cloud != nil
    session, err := c.prepareDirect(ctx, target, false)
    if err != nil {
        return err
    }
    defer session.close()
    target = session.target
    args := []string{"-o", "ForkAfterAuthentication=no", "-o", "RemoteCommand=none", "-o", "SessionType=default", "-t"}
    if cloudTarget {
        // The prepared provider session owns host trust; this native terminal
        // still owns password and key prompts rather than askpass.
        session.env = append(withoutEnv(session.env, "SSH_ASKPASS_REQUIRE"), "SSH_ASKPASS_REQUIRE=never")
    }
    // Unlike options, interactive login intentionally allows OpenSSH prompts and PTY.
    args = append(args, target.Args...)
    args = append(args, "--", target.Destination)
    cmd := foregroundProcess(ctx, c.Binary, args...)
    cmd.Env = session.env
    diagnostic := &diagnosticWriter{out: stderr}
    cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, diagnostic
    if err := cmd.Run(); err != nil {
        if ctx.Err() != nil {
            err = errors.Join(err, ctx.Err())
        }
        return fault.Wrap("SSH_FAILED", "interactive_failed", classifyConnection(err, diagnostic.data))
    }
    return nil
}

// Forward keeps a local loopback forwarding on its own foreground SSH connection.
// No ControlMaster socket is used or closed, even if SSH Config enables sharing.
func (c *Client) Forward(ctx context.Context, target config.Target, local, remote int, batch bool, stderr io.Writer) error {
    session, err := c.prepareDirect(ctx, target, batch)
    if err != nil {
        return err
    }
    defer session.close()
    target, batch = session.target, session.batch
    if local < 1 || local > 65535 || remote < 1 || remote > 65535 {
        return fault.New("BAD_CONFIG", "forward_port", nil)
    }
    args := forwardArguments(target, local, remote, batch)
    cmd := foregroundProcess(ctx, c.Binary, args...)
    cmd.Env = session.env
    diagnostic := &diagnosticWriter{out: stderr}
    cmd.Stderr = diagnostic
    err = cmd.Run()
    if errors.Is(ctx.Err(), context.Canceled) {
        return nil
    }
    if err != nil {
        return fault.Wrap("SSH_FAILED", "forward_failed", classifyConnection(err, diagnostic.data))
    }
    return nil
}

// Give native SSH an opportunity to restore terminal modes before forced exit.
func foregroundProcess(ctx context.Context, binary string, args ...string) *process {
    cmd := commandContext(ctx, binary, args...)
    cancelForeground(cmd)
    return cmd
}

func forwardArguments(target config.Target, local, remote int, batch bool) []string {
    args := []string{
        "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no",
        "-o", "ForkAfterAuthentication=no", "-o", "ExitOnForwardFailure=yes",
        "-o", "RemoteCommand=none", "-N", "-L", "127.0.0.1:" + strconv.Itoa(local) + ":127.0.0.1:" + strconv.Itoa(remote),
    }
    args = append(args, options(target, batch)...)
    return append(args, "--", target.Destination)
}

func execCommand(args []string, cwd string) (string, error) {
    if len(args) == 0 || args[0] == "" || strings.ContainsRune(cwd, 0) {
        return "", fault.New("BAD_CONFIG", "exec_command", nil)
    }
    words := []string{"/bin/sh", "-c", execDirectory, "loaf-exec", cwd}
    for _, arg := range args {
        if strings.ContainsRune(arg, 0) {
            return "", fault.New("BAD_CONFIG", "exec_command", nil)
        }
        words = append(words, arg)
    }
    for i := range words {
        words[i] = Quote(words[i])
    }
    return strings.Join(words, " "), nil
}

const execDirectory = `directory=$1
shift
case "$directory" in
    ''|'~') directory=$HOME ;;
    '~/'*) directory="$HOME/${directory#\~/}" ;;
    /*) ;;
    *) directory="$HOME/$directory" ;;
esac
CDPATH= cd -- "$directory" || exit 1
exec "$@"`
