package integration

import (
    "bufio"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync/atomic"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
    sources "github.com/elebirds/loaf/internal/sync"
)

func TestCommandScopedCloudConnections(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    for _, password := range []bool{false, true} {
        name := "key"
        if password {
            name = "password"
        }
        t.Run(name, func(t *testing.T) {
            f := newSSHFixture(t, password)
            ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
            defer cancel()
            // A separate user-owned master must neither be borrowed nor closed.
            userDir, err := os.MkdirTemp("/tmp", "loaf-user-test-")
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { _ = os.RemoveAll(userDir) })
            userSocket := filepath.Join(userDir, "control")
            user := exec.Command("ssh", "-F", f.config, "-M", "-N", "-S", userSocket, "-o", "ControlPersist=no", "fixture")
            if err := user.Start(); err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { _ = user.Process.Kill(); _ = user.Wait() })
            userAlive := func() bool {
                return exec.CommandContext(ctx, "ssh", "-S", userSocket, "-O", "check", "fixture").Run() == nil
            }
            wait(t, 5*time.Second, userAlive)
            before := strings.Count(command(t, "docker", "logs", f.id), "Accepted ")
            resolved := f.target
            if password {
                data, err := os.ReadFile(f.config)
                if err != nil {
                    t.Fatal(err)
                }
                passwordConfig := filepath.Join(f.dir, "password-config")
                data = []byte(strings.ReplaceAll(string(data), f.key, filepath.Join(f.dir, "no-key")))
                if err := os.WriteFile(passwordConfig, data, 0600); err != nil {
                    t.Fatal(err)
                }
                resolved.Args = []string{"-F", passwordConfig}
            }
            const tokenEnv = "LOAF_CONNECTION_TEST_TOKEN"
            t.Setenv(tokenEnv, "fixture-platform-token")
            binding := &cloud.Binding{
                Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "fixture", ID: "saved-instance"},
                Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: tokenEnv}},
            }
            target := config.Target{Cloud: binding, RemoteBase: "~/projects"}
            var resolutions atomic.Int32
            client := &ssh.Client{Binary: "ssh", AskpassExecutable: os.Getenv("LOAF_BIN"), SensitiveEnv: []string{tokenEnv}}
            client.Resolve = func(_ context.Context, saved config.Target) (config.Target, string, error) {
                resolutions.Add(1)
                if saved.Cloud.Ref.ID != "saved-instance" {
                    return config.Target{}, "", errors.New("saved instance redirected")
                }
                secret := ""
                if password {
                    secret = "loaf-fixture-password"
                }
                return resolved, secret, nil
            }
            openScope := func() *ssh.Connections {
                scope := ssh.NewConnections()
                client.Connections = scope
                t.Cleanup(scope.Close)
                return scope
            }
            scope := openScope()
            bootstrap := execution.Bootstrap{Remote: client}
            prepare := func(ctx context.Context, target config.Target, arch string, batch bool) (string, error) {
                var err error
                if arch == "" {
                    arch, err = client.RunnerArch(ctx, target, batch)
                    if err != nil {
                        return "", err
                    }
                }
                info, err := bootstrap.Verify(ctx, target, arch, batch)
                return info.Path, err
            }
            sender := ssh.Rsync{Client: client, Binary: "rsync"}
            store := local.New(t.TempDir())
            root := t.TempDir()
            project, err := store.Select(ctx, root, "")
            if err != nil {
                t.Fatal(err)
            }
            sourceFile(t, root, "experiment.sh", "set -eu\ntest -z \"${LOAF_CONNECTION_TEST_TOKEN-}\"\nmkdir outputs\necho scoped-ready\nwhile [ ! -f \"$HOME/scoped-release-"+project.ID+"\" ]; do sleep 0.05; done\nprintf collected >outputs/result.txt\necho scoped-run\n")
            plan, err := sources.Select(ctx, root, sources.SelectOptions{Mode: "mirror"})
            if err != nil {
                t.Fatal(err)
            }
            file := config.ProjectFile{}
            file.Outputs.Paths = []string{"outputs"}
            service := execution.Service{Index: store, Check: integrationPreflight{client}, Runner: prepare, Send: sender, Remote: client}
            submitted, err := service.Submit(ctx, execution.SubmitInput{Project: project, File: file, Target: "cloud", Connection: target, RemoteDir: "~/projects/" + project.ID, Plan: plan, Args: []string{"/bin/sh", "experiment.sh"}, Batch: true}, nil)
            if err != nil || submitted.Submission != "submitted" {
                t.Fatal(submitted, err)
            }
            wait(t, 5*time.Second, func() bool {
                observed, err := service.Observe(ctx, submitted.Ref, 0, 100, true)
                return err == nil && observed.Log != nil && strings.Contains(observed.Log.Text, "scoped-ready")
            })
            canceled, stopObservation := context.WithCancel(ctx)
            stopObservation()
            observed, err := service.Observe(canceled, submitted.Ref, 30*time.Second, 100, true)
            if err == nil || observed.Submission != "submitted" || resolutions.Load() != 1 {
                t.Fatal(observed, resolutions.Load(), err)
            }
            scope.Close()
            accepted := strings.Count(command(t, "docker", "logs", f.id), "Accepted ")
            if accepted-before != 1 || !userAlive() {
                t.Fatal("run reauthenticated or closed user's master", accepted-before, resolutions.Load())
            }
            // Closing the canceled observation's master must leave the accepted
            // supervisor alive. A fresh invocation queries that same ID only.
            before = accepted
            scope = openScope()
            observed, err = service.Observe(ctx, submitted.Ref, 0, 100, true)
            if err != nil || observed.Status != "running" || observed.Submission != "submitted" {
                t.Fatal("canceling the connection stopped the accepted run", observed, err)
            }
            f.shell(`touch "$HOME/scoped-release-$1"`, project.ID)
            result, err := service.Observe(ctx, submitted.Ref, 5*time.Second, 100, true)
            if err != nil || result.Status != "succeeded" || result.Log == nil || !strings.Contains(result.Log.Text, "scoped-run") || resolutions.Load() != 2 {
                t.Fatal(result, resolutions.Load(), err)
            }
            scope.Close()
            accepted = strings.Count(command(t, "docker", "logs", f.id), "Accepted ")
            if accepted-before != 1 || !userAlive() {
                t.Fatal("recovery reauthenticated or closed user's master", accepted-before)
            }
            // A second invocation resolves again, but all five pull phases still
            // use just one authentication and the original frozen run binding.
            before = accepted
            scope = openScope()
            puller := sources.PullService{Remote: client, Runner: prepare, Receive: sender}
            destination, err := filepath.EvalSymlinks(t.TempDir())
            if err != nil {
                t.Fatal(err)
            }
            pulled, err := puller.Pull(ctx, submitted.Ref, destination, []string{"outputs"}, config.PullPolicy{}, true)
            contents, readErr := os.ReadFile(filepath.Join(destination, "outputs", "result.txt"))
            if err != nil || readErr != nil || string(contents) != "collected" || len(pulled.DownloadedPaths) != 1 || resolutions.Load() != 3 {
                t.Fatal(pulled, string(contents), resolutions.Load(), err, readErr)
            }
            scope.Close()
            accepted = strings.Count(command(t, "docker", "logs", f.id), "Accepted ")
            if accepted-before != 1 || !userAlive() {
                t.Fatal("pull reauthenticated or closed user's master", accepted-before)
            }
            // Even a usable password and a live unrelated master cannot bypass
            // unknown-host rejection on a fresh private connection.
            known, err := os.ReadFile(f.known)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { _ = os.WriteFile(f.known, known, 0600) })
            if err := os.WriteFile(f.known, nil, 0600); err != nil {
                t.Fatal(err)
            }
            scope = openScope()
            _, err = client.Execute(ctx, target, "true", nil, true)
            var rejected *fault.Error
            if !errors.As(err, &rejected) || rejected.Code != "SSH_FAILED" || fault.Stage(err) != "ssh_connect" || !strings.Contains(rejected.Params["Detail"].(string), "Host key verification failed") {
                t.Fatal("private connection bypassed host trust", err)
            }
            scope.Close()
            if !userAlive() {
                t.Fatal("failed private connection closed user's master")
            }
        })
    }
}

func TestCommandScopedCloudConnectionPreservesExplicitJump(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    target := config.Target{RemoteBase: "~/projects", Cloud: &cloud.Binding{
        Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "fixture", ID: "jumped-instance"},
        Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "LOAF_JUMP_FIXTURE_TOKEN"}},
    }}
    scope := ssh.NewConnections()
    defer scope.Close()
    calls := 0
    client := &ssh.Client{Binary: "ssh", Connections: scope, Resolve: func(_ context.Context, saved config.Target) (config.Target, string, error) {
        calls++
        return config.Target{Destination: "jumped", Args: []string{"-F", f.config, "-J", "fixture"}}, "", nil
    }}
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    for range 2 {
        out, err := client.Execute(ctx, target, "printf jumped", nil, true)
        if err != nil || string(out) != "jumped" {
            t.Fatal(string(out), err)
        }
    }
    if calls != 1 {
        t.Fatal("resolved per channel", calls)
    }
}

func TestCommandConnectionParentLifetime(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    if os.Getenv("LOAF_CONNECTION_LIFETIME_CHILD") == "1" {
        var resolved config.Target
        if err := json.Unmarshal([]byte(os.Getenv("LOAF_CONNECTION_LIFETIME_TARGET")), &resolved); err != nil {
            t.Fatal(err)
        }
        scope := ssh.NewConnections()
        defer scope.Close()
        target := config.Target{RemoteBase: "~/projects", Cloud: &cloud.Binding{
            Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "fixture", ID: "lifetime-instance"},
            Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "LOAF_LIFETIME_FIXTURE_TOKEN"}},
        }}
        client := &ssh.Client{Binary: "ssh", Connections: scope, Resolve: func(context.Context, config.Target) (config.Target, string, error) { return resolved, "", nil }}
        if _, err := client.Execute(context.Background(), target, "true", nil, true); err != nil {
            t.Fatal(err)
        }
        fmt.Fprintln(os.Stdout, "cloud-scope-ready")
        _, _ = io.Copy(io.Discard, os.Stdin)
        return
    }
    f := newFixture(t)
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    helper := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommandConnectionParentLifetime$")
    target, err := json.Marshal(f.target)
    if err != nil {
        t.Fatal(err)
    }
    helper.Env = append(os.Environ(), "LOAF_CONNECTION_LIFETIME_CHILD=1", "LOAF_CONNECTION_LIFETIME_TARGET="+string(target))
    hold, err := helper.StdinPipe()
    if err != nil {
        t.Fatal(err)
    }
    defer hold.Close()
    output, err := helper.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    if err := helper.Start(); err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })
    ready, err := bufio.NewReader(output).ReadString('\n')
    if err != nil || strings.TrimSpace(ready) != "cloud-scope-ready" {
        t.Fatal(ready, err)
    }
    if f.shell(`pgrep -u researcher -x cat || true`) == "" {
        t.Fatal("master lifetime tether was not established")
    }
    if err := helper.Process.Kill(); err != nil {
        t.Fatal(err)
    }
    _ = helper.Wait()
    wait(t, 5*time.Second, func() bool { return f.shell(`pgrep -u researcher -x cat || true`) == "" })
}
