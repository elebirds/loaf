package main

import (
    "context"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "syscall"

    "github.com/elebirds/loaf/internal/autodl"
    "github.com/elebirds/loaf/internal/cli"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/credentials"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
    "golang.org/x/term"
)

type unavailableSSH struct{}

func (unavailableSSH) Check(context.Context, config.Target, string, bool) (ssh.Report, error) {
    return ssh.Report{}, fault.New("SSH_FAILED", "ssh_missing", nil)
}

func main() {
    if len(os.Args) == 3 && os.Args[1] == "__ssh-askpass" {
        os.Exit(ssh.AskpassMain(os.Args[2], os.Stdout))
    }
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
    var runner cli.RunnerVerifier
    var syncer cli.Synchronizer
    var cleaner cli.RunCleaner
    var puller cli.OutputPuller
    var runs *execution.Service
    var operations cli.MachineOperations
    store := config.New(home, os.Getenv("XDG_CONFIG_HOME"), os.Getenv("XDG_STATE_HOME"))
    authentication := &cloud.Authentication{Keyring: credentials.Keyring{}, Files: credentials.File{Dir: filepath.Join(filepath.Dir(store.IndexFile), "credentials")}}
    factory := autodl.Factory{LookupEnv: os.LookupEnv, ReadSecret: authentication.Token}
    authentication.Authenticate = factory.Login
    cloudService := cli.DefaultCloudService(store, factory.Open)
    if binary, err := exec.LookPath("ssh"); err == nil {
        client := &ssh.Client{Binary: binary}
        client.Resolve = cloudConnection(factory.Open)
        if cfg, err := store.Load(); err == nil {
            for _, account := range cfg.Cloud.Accounts {
                if account.Credential.Env != "" {
                    client.SensitiveEnv = append(client.SensitiveEnv, account.Credential.Env)
                }
            }
        }
        checker = client
        operations = client
        bootstrap := execution.Bootstrap{Remote: client}
        runner = bootstrap
        rsync, _ := exec.LookPath("rsync")
        prepare := func(ctx context.Context, target config.Target, arch string, batch bool) (string, error) {
            if arch == "" {
                var err error
                arch, err = client.RunnerArch(ctx, target, batch)
                if err != nil {
                    return "", err
                }
            }
            info, err := bootstrap.Verify(ctx, target, arch, batch)
            return info.Path, err
        }
        sender := ssh.Rsync{Client: client, Binary: rsync}
        runs = &execution.Service{Index: store, Check: executionCheck{client}, Runner: prepare, Send: sender, Remote: client}
        syncer = sources.Service{Check: client, Runner: prepare, Send: sender}
        cleaner = execution.CleanupService{Runs: runs, Index: store, Remote: client, Runner: prepare}
        puller = sources.PullService{Remote: client, Runner: prepare, Receive: sender}
    }
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()
    code := cli.Execute(ctx, os.Args[1:], cli.Options{Store: store, Cloud: cloudService, Auth: authentication, ReadPassword: terminalPassword, Runs: runs, Checker: checker, Runner: runner, Syncer: syncer, Puller: puller, Cleaner: cleaner, Operations: operations, CWD: cwd, Env: env, In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Interactive: term.IsTerminal(int(os.Stdin.Fd()))})
    os.Exit(code)
}

// executionCheck limits automatic checks to the current operation's prerequisites.
type executionCheck struct{ client *ssh.Client }

func (c executionCheck) Check(ctx context.Context, target config.Target, dir string, batch bool) (ssh.Report, error) {
    return c.client.Preflight(ctx, target, dir, batch)
}
