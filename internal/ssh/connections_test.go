package ssh

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func scopedFixture(t *testing.T) (*Client, config.Target, *atomic.Int32, string) {
    t.Helper()
    events := filepath.Join(t.TempDir(), "events")
    t.Setenv("LOAF_TEST_EVENTS", events)
    t.Setenv("LOAF_TEST_PLATFORM_TOKEN", "must-not-reach-ssh")
    fake := filepath.Join(t.TempDir(), "ssh")
    script := `#!/bin/sh
socket=; master=; operation=; previous=
for argument do
    case "$argument" in
        ControlPath=*) if [ -z "$socket" ]; then socket=${argument#ControlPath=}; fi ;;
        ControlMaster=*) if [ -z "$master" ]; then master=${argument#ControlMaster=}; fi ;;
    esac
    case "$previous" in -S) socket=$argument ;; -O) operation=$argument ;; esac
    previous=$argument
done
if [ "$operation" = check ]; then test -f "$socket"; exit; fi
if [ -n "${LOAF_TEST_PLATFORM_TOKEN-}" ]; then echo leaked-token >&2; exit 1; fi
if [ "$master" = yes ]; then
    echo master >> "$LOAF_TEST_EVENTS"
    : > "$socket"
    trap 'rm -f "$socket"' EXIT
    cat >/dev/null
else
    if [ ! -f "$socket" ]; then echo 'Connection closed: private master unavailable' >&2; exit 255; fi
    echo request >> "$LOAF_TEST_EVENTS"
    printf ok
fi
`
    if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
        t.Fatal(err)
    }
    target := config.Target{RemoteBase: "~/projects", Cloud: &cloud.Binding{
        Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "fixture", ID: "original-instance"},
        Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "LOAF_TEST_PLATFORM_TOKEN"}},
    }}
    calls := &atomic.Int32{}
    client := &Client{Binary: fake, Connections: NewConnections(), Resolve: func(_ context.Context, saved config.Target) (config.Target, string, error) {
        calls.Add(1)
        if saved.Cloud.Ref.ID != "original-instance" {
            return config.Target{}, "", errors.New("redirected frozen instance")
        }
        saved.Cloud = nil
        saved.Destination = "root@fixture"
        saved.Args = []string{"-p", "2222"}
        return saved, "", nil
    }}
    t.Cleanup(client.Connections.Close)
    return client, target, calls, events
}

func TestCommandConnectionsResolveAndAuthenticateOnceConcurrently(t *testing.T) {
    c, target, calls, events := scopedFixture(t)
    var wg sync.WaitGroup
    failures := make(chan error, 8)
    for range 8 {
        wg.Add(1)
        go func() {
            defer wg.Done()
            out, err := c.Execute(context.Background(), target, "fixed-probe", nil, true)
            if err != nil {
                failures <- err
            } else if string(out) != "ok" {
                failures <- errors.New("wrong response")
            }
        }()
    }
    wg.Wait()
    close(failures)
    for err := range failures {
        t.Error(err)
    }
    data, err := os.ReadFile(events)
    if err != nil || calls.Load() != 1 || strings.Count(string(data), "master\n") != 1 || strings.Count(string(data), "request\n") != 8 {
        t.Fatal(calls.Load(), string(data), err)
    }
    session, err := c.prepare(context.Background(), target, true)
    if err != nil {
        t.Fatal(err)
    }
    args := strings.Join(arguments(session.target, "fixed-probe", session.batch), " ")
    for _, required := range []string{"ProxyCommand=false", "ProxyJump=none", "BatchMode=yes", "StrictHostKeyChecking=yes"} {
        if !strings.Contains(args, required) {
            t.Fatal(args)
        }
    }
    socket := privateSocket(session)
    info, err := os.Stat(filepath.Dir(socket))
    if err != nil || info.Mode().Perm() != 0700 {
        t.Fatal(info, err)
    }
    c.Connections.Close()
    if _, err := os.Stat(filepath.Dir(socket)); !os.IsNotExist(err) {
        t.Fatal("private socket directory retained", err)
    }
    if target.Cloud == nil || target.Destination != "" {
        t.Fatal("mutated saved binding", target)
    }
}

func TestPrivateMuxRefusesFallbackAndOtherCommandsRemainIndependent(t *testing.T) {
    c, target, calls, _ := scopedFixture(t)
    session, err := c.prepare(context.Background(), target, true)
    if err != nil {
        t.Fatal(err)
    }
    socket := privateSocket(session)
    if err := os.Remove(socket); err != nil {
        t.Fatal(err)
    }
    if _, err := c.Execute(context.Background(), target, "mutation", nil, true); err == nil || calls.Load() != 1 {
        t.Fatal("silently reconnected", calls.Load(), err)
    }
    other := *c
    other.Connections = NewConnections()
    defer other.Connections.Close()
    if _, err := other.Execute(context.Background(), target, "probe", nil, true); err != nil || calls.Load() != 2 {
        t.Fatal("another command reused the master", calls.Load(), err)
    }
    other.Connections.Close()
    if _, err := c.Execute(context.Background(), target, "mutation", nil, true); err == nil {
        t.Fatal("another command redirected this one")
    }
}

func TestStaticConnectionsDoNotAcquireNewPreconditions(t *testing.T) {
    c, _, calls, _ := scopedFixture(t)
    target := config.Target{Destination: "native-alias", Args: []string{"-F", "/existing/config"}}
    session, err := c.prepare(context.Background(), target, true)
    if err != nil || calls.Load() != 0 || session.target.Destination != target.Destination || strings.Join(session.target.Args, " ") != strings.Join(target.Args, " ") {
        t.Fatal(session, calls.Load(), err)
    }
    session.close()
    c.Connections.Close()
    if _, err := c.prepare(context.Background(), target, true); err != nil {
        t.Fatal("static target depends on cloud scope", err)
    }
}

func TestCloudLookupFailureRetainsItsStageAndDoesNotCreateMaster(t *testing.T) {
    c, target, _, events := scopedFixture(t)
    original := fault.New("CLOUD_FAILED", "cloud_failed", map[string]any{"ProviderCode": "TRANSPORT"})
    c.Resolve = func(context.Context, config.Target) (config.Target, string, error) {
        return config.Target{}, "", original
    }
    _, err := c.Execute(context.Background(), target, "mutation", nil, true)
    if !errors.Is(err, original) || fault.Stage(err) != "cloud_connection" {
        t.Fatal(err)
    }
    if _, err := os.Stat(events); !os.IsNotExist(err) {
        t.Fatal("started SSH after failed lookup", err)
    }
}

func privateSocket(session connectionSession) string {
    for _, arg := range session.target.Args {
        if strings.HasPrefix(arg, "ControlPath=") {
            return strings.TrimPrefix(arg, "ControlPath=")
        }
    }
    return ""
}

func TestMuxArgumentsRetainPreferencesButNotTheMastersJumpChain(t *testing.T) {
    args := []string{"-F", "spaced config", "-J", "jump-a", "-Jjump-b", "-o", "ProxyJump=jump-c", "-oProxyJump jump-d", "-p", "22", "-o", "IdentitiesOnly=yes"}
    want := []string{"-F", "spaced config", "-p", "22", "-o", "IdentitiesOnly=yes"}
    if got := muxArguments(args); !reflect.DeepEqual(got, want) {
        t.Fatal(got)
    }
}

func TestWaitingForSharedAuthenticationIsCancellable(t *testing.T) {
    c, target, _, _ := scopedFixture(t)
    original := c.Resolve
    entered, release := make(chan struct{}), make(chan struct{})
    c.Resolve = func(ctx context.Context, target config.Target) (config.Target, string, error) {
        close(entered)
        <-release
        return original(ctx, target)
    }
    first := make(chan error, 1)
    go func() {
        _, err := c.prepare(context.Background(), target, true)
        first <- err
    }()
    <-entered
    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()
    second := make(chan error, 1)
    go func() {
        _, err := c.prepare(ctx, target, true)
        second <- err
    }()
    select {
    case err := <-second:
        if !errors.Is(err, context.DeadlineExceeded) {
            t.Error("lost canceled wait", err)
        }
    case <-time.After(time.Second):
        t.Error("canceled query waited for another query's authentication")
    }
    close(release)
    if err := <-first; err != nil {
        t.Fatal(err)
    }
}
