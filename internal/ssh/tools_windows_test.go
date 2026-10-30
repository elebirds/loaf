package ssh

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func TestWindowsRequiresOnePairedToolInstallation(t *testing.T) {
    root := filepath.Join(t.TempDir(), "工具 space")
    bin := filepath.Join(root, "usr", "bin")
    if err := os.MkdirAll(bin, 0700); err != nil {
        t.Fatal(err)
    }
    for _, name := range []string{"msys-2.0.dll", "ssh.exe", "rsync.exe", "cygpath.exe"} {
        if err := os.WriteFile(filepath.Join(bin, name), nil, 0600); err != nil {
            t.Fatal(err)
        }
    }
    if err := os.WriteFile(filepath.Join(root, "msys2_shell.cmd"), nil, 0600); err != nil {
        t.Fatal(err)
    }
    t.Setenv("LOAF_MSYS2_ROOT", root)
    selected, err := DiscoverTools()
    if err != nil || selected.SSH != filepath.Join(bin, "ssh.exe") || selected.Rsync != filepath.Join(bin, "rsync.exe") {
        t.Fatal(selected, err)
    }
    t.Setenv("LOAF_MSYS2_ROOT", "")
    t.Setenv("PATH", bin)
    inferred, err := DiscoverTools()
    if err != nil || inferred != selected {
        t.Fatal(inferred, err)
    }
    if err := os.Remove(filepath.Join(bin, "ssh.exe")); err != nil {
        t.Fatal(err)
    }
    _, err = DiscoverTools()
    var problem *fault.Error
    if !errors.As(err, &problem) || problem.Code != "SSH_FAILED" || problem.Message != "msys2_missing" {
        t.Fatal(err)
    }
    t.Setenv("LOAF_MSYS2_ROOT", "relative")
    if _, err := DiscoverTools(); err == nil {
        t.Fatal("accepted relative tool root")
    }
}

func TestWindowsConnectionsRefreshFrozenIdentityWithoutMasters(t *testing.T) {
    trace := &Trace{}
    trace.Enable()
    scope := NewConnections()
    defer scope.Close()
    target := config.Target{Cloud: &cloud.Binding{
        Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "fixture", ID: "frozen-instance"},
        Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "LOAF_NATIVE_TEST_TOKEN"}},
    }}
    t.Setenv("loaf_native_test_token", "must-not-reach-ssh")
    t.Setenv("SHELL", "powershell.exe")
    original := []string{"-F", `C:\用户 space\config`, "-J", "jump"}
    count := 0
    client := Client{Connections: scope, Trace: trace, Resolve: func(_ context.Context, saved config.Target) (config.Target, string, error) {
        count++
        if !reflect.DeepEqual(saved, target) {
            t.Fatal("frozen target changed")
        }
        return config.Target{Destination: "resolved", Args: original}, "", nil
    }}
    for range 2 {
        session, err := client.prepare(context.Background(), target, true)
        if err != nil {
            t.Fatal(err)
        }
        if !containsArgument(session.target.Args, "ControlMaster=no") || !containsArgument(session.target.Args, "ControlPath=none") || !containsArgument(session.target.Args, "C:/用户 space/config") {
            t.Fatal(session.target.Args)
        }
        for _, value := range session.env {
            if strings.Contains(value, "must-not-reach-ssh") {
                t.Fatal("platform token leaked")
            }
        }
        if !containsArgument(session.env, "SHELL=/usr/bin/sh") {
            t.Fatal("Windows shell used for POSIX proxy command")
        }
        session.close()
    }
    if count != 2 || trace.Snapshot().ConnectionResolutions != 2 || trace.Snapshot().MastersEstablished != 0 {
        t.Fatal(count, trace.Snapshot())
    }
    if original[1] != `C:\用户 space\config` {
        t.Fatal("saved connection arguments mutated")
    }
    scope.Close()
    if _, err := client.prepare(context.Background(), target, true); err == nil || count != 2 {
        t.Fatal("closed scope reused", err)
    }
}
