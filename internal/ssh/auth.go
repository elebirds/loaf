package ssh

import (
    "context"
    "os"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// ResolveConnection refreshes a bound instance without redirecting its identity.
// The returned password lives only for one native SSH/rsync invocation.
type ResolveConnection func(context.Context, config.Target) (config.Target, string, error)

type connectionSession struct {
    target     config.Target
    batch      bool
    env        []string
    close      func()
    masterDone <-chan struct{}
}

// scopedTransportError keeps the identity of the private master behind a failed
// operation. Native mux clients can exit 255 without any diagnostic when that
// master disappears; an exit code alone cannot distinguish a disconnection.
type scopedTransportError struct {
    cause      error
    masterDone <-chan struct{}
}

func (e *scopedTransportError) Error() string { return e.cause.Error() }
func (e *scopedTransportError) Unwrap() error { return e.cause }

func (s connectionSession) transportError(err error) error {
    if err == nil || s.masterDone == nil {
        return err
    }
    return &scopedTransportError{cause: err, masterDone: s.masterDone}
}

func (e *scopedTransportError) disconnected() bool {
    // The mux client and the master's Wait goroutine may finish in either
    // order. Synchronize briefly on our own master, not arbitrary SSH exits.
    timer := time.NewTimer(25 * time.Millisecond)
    defer timer.Stop()
    select {
    case <-e.masterDone:
        return true
    case <-timer.C:
        return false
    }
}

func (c *Client) prepare(ctx context.Context, target config.Target, batch bool) (connectionSession, error) {
    if c.Connections != nil && target.Cloud != nil {
        if err := Validate(target); err != nil {
            return connectionSession{}, err
        }
        return c.Connections.prepare(ctx, c, target, batch)
    }
    return c.prepareDirect(ctx, target, batch)
}

func (c *Client) prepareDirect(ctx context.Context, target config.Target, batch bool) (session connectionSession, err error) {
    session = connectionSession{target: target, batch: batch, close: func() {}}
    defer func() {
        if err == nil {
            session = platformSession(session)
        }
    }()
    if c.ToolError != nil {
        return session, c.ToolError
    }
    if err := Validate(target); err != nil {
        return session, err
    }
    if len(c.SensitiveEnv) > 0 {
        session.env = os.Environ()
        for _, key := range c.SensitiveEnv {
            session.env = withoutEnv(session.env, key)
        }
    }
    if target.Cloud == nil {
        return session, nil
    }
    if c.Resolve == nil {
        return session, fault.New("CLOUD_FAILED", "cloud_failed", nil)
    }
    c.Trace.resolved()
    finish := c.Trace.stage("cloud_connection")
    resolved, password, err := c.Resolve(ctx, target)
    finish(err)
    if err != nil {
        return session, fault.At(err, "cloud_connection")
    }
    if resolved.Cloud != nil {
        return session, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    if err := Validate(resolved); err != nil {
        return session, err
    }
    session.target = resolved
    // Even a user's broad SendEnv must not forward a platform token to training.
    if session.env == nil {
        session.env = os.Environ()
    }
    if target.Cloud.Settings.Credential.Env != "" {
        session.env = withoutEnv(session.env, target.Cloud.Settings.Credential.Env)
    }
    session.target.Args = append([]string{"-o", "StrictHostKeyChecking=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no"}, session.target.Args...)
    if password == "" {
        return session, nil
    }
    askpass, cleanup, err := passwordPrompt(ctx, password, c.AskpassExecutable)
    if err != nil {
        return session, err
    }
    session.close = cleanup
    for _, key := range []string{"SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", "LOAF_ASKPASS_SOCKET"} {
        session.env = withoutEnv(session.env, key)
    }
    session.env = append(session.env, askpass...)
    session.batch = false // BatchMode=yes disables OpenSSH's askpass support.
    session.target.Args = append([]string{"-o", "BatchMode=no", "-o", "NumberOfPasswordPrompts=1", "-o", "PreferredAuthentications=publickey,password,keyboard-interactive"}, session.target.Args...)
    return session, nil
}

func withoutEnv(env []string, key string) []string {
    out := make([]string, 0, len(env))
    for _, entry := range env {
        name, _, _ := strings.Cut(entry, "=")
        if !strings.EqualFold(name, key) {
            out = append(out, entry)
        }
    }
    return out
}
