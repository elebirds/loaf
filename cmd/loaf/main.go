package main

import (
    "context"
    "os"
    "os/exec"
    "os/signal"
    "syscall"

    "github.com/elebirds/loaf/internal/cli"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "golang.org/x/term"
)

type unavailableSSH struct{}

func (unavailableSSH) Check(context.Context, config.Target, string, bool) (ssh.Report, error) {
    return ssh.Report{}, fault.New("SSH_FAILED", "ssh_missing", nil)
}

func main() {
    home, err := os.UserHomeDir()
    if err != nil {
        os.Exit(1)
    }
    cwd, err := os.Getwd()
    if err != nil {
        os.Exit(1)
    }
    env := map[string]string{}
    for _, k := range []string{"LOAF_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
        env[k] = os.Getenv(k)
    }
    var checker cli.Checker = unavailableSSH{}
    if binary, err := exec.LookPath("ssh"); err == nil {
        client := &ssh.Client{Binary: binary}
        checker = client
    }
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()
    code := cli.Execute(ctx, os.Args[1:], cli.Options{Store: config.New(home, os.Getenv("XDG_CONFIG_HOME"), os.Getenv("XDG_STATE_HOME")), Checker: checker, CWD: cwd, Env: env, In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Interactive: term.IsTerminal(int(os.Stdin.Fd()))})
    os.Exit(code)
}
