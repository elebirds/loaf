package ssh

import (
    "bytes"
    "context"
    "errors"
    "os/exec"
    "time"
)

// process preserves os/exec's argument and stream handling while giving each
// Windows invocation ownership of its complete local process tree.
type process struct {
    *exec.Cmd
    close func()
}

type processOwner struct {
    close   func()
    started func() error
}

func commandContext(ctx context.Context, binary string, args ...string) *process {
    cmd := exec.CommandContext(ctx, binary, args...)
    cmd.WaitDelay = 2 * time.Second
    return &process{Cmd: cmd}
}

func (p *process) Start() error {
    owner, err := ownProcess(p.Cmd)
    if err != nil {
        return err
    }
    p.close = owner.close
    if err := p.Cmd.Start(); err != nil {
        owner.close()
        return err
    }
    if err := owner.started(); err != nil {
        p.Cmd.Process.Kill()
        p.Cmd.Wait()
        owner.close()
        return err
    }
    return nil
}

func (p *process) Wait() error {
    defer p.close()
    return p.Cmd.Wait()
}

func (p *process) Run() error {
    if err := p.Start(); err != nil {
        return err
    }
    return p.Wait()
}

func (p *process) Output() ([]byte, error) {
    if p.Stdout != nil {
        return nil, errors.New("ssh: stdout already set")
    }
    var output, diagnostic bytes.Buffer
    p.Stdout = &output
    capture := p.Stderr == nil
    if capture {
        p.Stderr = &diagnostic
    }
    err := p.Run()
    var exit *exec.ExitError
    if capture && errors.As(err, &exit) {
        exit.Stderr = diagnostic.Bytes()
    }
    return output.Bytes(), err
}

func (p *process) CombinedOutput() ([]byte, error) {
    if p.Stdout != nil || p.Stderr != nil {
        return nil, errors.New("ssh: output already set")
    }
    var output bytes.Buffer
    p.Stdout, p.Stderr = &output, &output
    err := p.Run()
    return output.Bytes(), err
}
