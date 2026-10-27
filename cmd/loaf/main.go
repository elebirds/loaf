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
    application "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/credentials"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
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
    for _, k := range []string{"LOAF_LANG", "LC_ALL", "LC_MESSAGES", "LANG", "TERM"} {
        env[k] = os.Getenv(k)
    }
    var checker application.Checker = unavailableSSH{}
    var runner application.RunnerVerifier
    var syncer application.Synchronizer
    var cleaner application.RunCleaner
    var browser application.OutputBrowser
    var puller application.OutputPuller
    var runs *execution.Service
    var operations application.MachineOperations
    store := config.New(home, os.Getenv("XDG_CONFIG_HOME"), os.Getenv("XDG_STATE_HOME"))
    settings, configErr := store.Load()
    state := local.New(store.StateDir)
    authentication := &cloud.Authentication{Keyring: credentials.Keyring{}, Files: credentials.File{Dir: filepath.Join(store.StateDir, "credentials")}}
    factory := autodl.Factory{LookupEnv: os.LookupEnv, ReadSecret: authentication.Token}
    authentication.Authenticate = factory.Login
    cloudService := &cloud.Service{Open: factory.Open, Receipts: cloud.Receipts{Dir: filepath.Dir(state.Path)}}
    connections := ssh.NewConnections()
    trace := &ssh.Trace{}
    if binary, err := exec.LookPath("ssh"); err == nil {
        client := &ssh.Client{Binary: binary, Connections: connections, Trace: trace}
        client.Resolve = cloudConnection(factory.Open)
        for _, account := range settings.Cloud.Accounts {
            if account.Credential.Env != "" {
                client.SensitiveEnv = append(client.SensitiveEnv, account.Credential.Env)
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
        runs = &execution.Service{Index: state, Metadata: state, Observations: state, Check: executionCheck{client}, Runner: prepare, Send: sender, Remote: client}
        syncer = sources.Service{Check: client, Runner: prepare, Send: sender}
        cleaner = execution.CleanupService{Runs: runs, Index: state, Remote: client, Runner: prepare}
        browser = execution.OutputBrowser{Remote: client, Runner: prepare}
        puller = sources.PullService{Remote: client, Runner: prepare, Receive: sender}
    }
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()
    app := application.New(application.Dependencies{
        Config: store, State: state, Trace: trace, Cloud: cloudService, Auth: authentication,
        Runs: runs, Checker: checker, Runner: runner, Syncer: syncer, Outputs: browser,
        Puller: puller, Cleaner: cleaner, Operations: operations, CWD: cwd,
    }, settings)
    code := cli.Execute(ctx, os.Args[1:], cli.Options{
        App: app, Language: settings.Language, InitError: configErr, Trace: trace,
        ReadPassword: terminalPassword, Env: env, In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
        TerminalOutput: term.IsTerminal(int(os.Stdout.Fd())), TerminalError: term.IsTerminal(int(os.Stderr.Fd())),
        Interactive: term.IsTerminal(int(os.Stdin.Fd())),
    })
    connections.Close()
    os.Exit(code)
}

// executionCheck limits automatic checks to the current operation's prerequisites.
type executionCheck struct{ client *ssh.Client }

func (c executionCheck) Check(ctx context.Context, target config.Target, dir string, batch bool) (ssh.Report, error) {
    return c.client.Preflight(ctx, target, dir, batch)
}
