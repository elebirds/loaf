package integration

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/ssh"
)

func TestWindowsFrozenCloudConnections(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newSSHFixture(t, true)
    data, err := os.ReadFile(f.config)
    if err != nil {
        t.Fatal(err)
    }
    data = []byte(strings.ReplaceAll(string(data), filepath.ToSlash(f.key), filepath.ToSlash(filepath.Join(f.dir, "missing-key"))))
    if err := os.WriteFile(f.config, data, 0600); err != nil {
        t.Fatal(err)
    }
    binding := &cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "pro", Account: "fixture", ID: "frozen-instance"}}
    binding.Settings = cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "LOAF_FROZEN_FIXTURE_TOKEN"}}
    target := config.Target{Cloud: binding}
    scope := ssh.NewConnections()
    defer scope.Close()
    trace := &ssh.Trace{}
    trace.Enable()
    client := *f.client
    client.Connections, client.Trace, client.AskpassExecutable = scope, trace, os.Getenv("LOAF_BIN")
    resolutions := 0
    client.Resolve = func(_ context.Context, saved config.Target) (config.Target, string, error) {
        resolutions++
        if saved.Cloud == nil || saved.Cloud.Ref != binding.Ref {
            return config.Target{}, "", errors.New("binding redirected")
        }
        return f.target, "loaf-fixture-password", nil
    }
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    before := strings.Count(command(t, "docker", "logs", f.id), "Accepted ")
    for range 2 {
        if _, err := client.Execute(ctx, target, "/bin/true", nil, true); err != nil {
            t.Fatal(err)
        }
    }
    report := trace.Snapshot()
    accepted := strings.Count(command(t, "docker", "logs", f.id), "Accepted ") - before
    if resolutions != 2 || accepted != 2 || report.MastersEstablished != 0 {
        t.Fatal(resolutions, accepted, report)
    }
    scope.Close()
    if _, err := client.Execute(ctx, target, "/bin/true", nil, true); err == nil {
        t.Fatal("closed command scope reused")
    }
}

func TestWindowsExplicitMSYSRootWithoutToolPATH(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := t.TempDir()
    if code, out := cli(t, project, "machine", "add", "jumped", "--ssh", "ssh -F "+ssh.Quote(f.config)+" jumped", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    t.Setenv("LOAF_MSYS2_ROOT", filepath.Dir(filepath.Dir(filepath.Dir(f.client.Binary))))
    t.Setenv("PATH", filepath.Join(os.Getenv("SystemRoot"), "System32"))
    // The documented explicit root must work without any MSYS binaries on PATH,
    // even when System32 contains the incompatible Windows OpenSSH client.
    if code, out := cli(t, project, "check", "--target", "jumped", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    sourceFile(t, project, "loaf.toml", "[sync]\nrespect_gitignore=false\n")
    sourceFile(t, project, "source 中文.txt", "paired tools")
    if code, out := cli(t, project, "sync", "--target", "jumped", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
}
