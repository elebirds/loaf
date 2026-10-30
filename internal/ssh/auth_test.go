package ssh

import (
    "bytes"
    "context"
    "os"
    "path/filepath"
    "runtime"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
)

func TestAskpassDoesNotPersistOrForwardTheSecret(t *testing.T) {
    secret := "fixture password 'quoted'"
    executable, _ := os.Executable()
    env, cleanup, err := passwordPrompt(context.Background(), secret, executable)
    if err != nil {
        t.Fatal(err)
    }
    defer cleanup()
    socket, launcher := "", ""
    for _, entry := range env {
        if strings.Contains(entry, secret) {
            t.Fatal("secret in child environment")
        }
        key, value, _ := strings.Cut(entry, "=")
        if key == "LOAF_ASKPASS_SOCKET" {
            socket = value
        }
        if key == "SSH_ASKPASS" {
            launcher = value
        }
    }
    if runtime.GOOS != "windows" {
        contents, err := os.ReadFile(launcher)
        if err != nil || bytes.Contains(contents, []byte(secret)) {
            t.Fatal("secret persisted in launcher", err)
        }
        mode, err := os.Stat(filepath.Dir(socket))
        if err != nil || mode.Mode().Perm() != 0700 {
            t.Fatal(mode, err)
        }
    } else if launcher != filepath.ToSlash(executable) {
        t.Fatal("askpass is not the native helper")
    }
    t.Setenv("LOAF_ASKPASS_SOCKET", socket)
    for _, prompt := range []string{"root@gpu.example.com's password:", "Password:"} {
        var out bytes.Buffer
        if code := AskpassMain(prompt, &out); code != 0 || out.String() != secret+"\n" {
            t.Fatal(code, out.String())
        }
    }
    for _, prompt := range []string{"Are you sure you want to continue connecting (yes/no/[fingerprint])?", "Enter passphrase for key:", "Verification code:", "Password:\n"} {
        var out bytes.Buffer
        if code := AskpassMain(prompt, &out); code == 0 || out.Len() != 0 {
            t.Fatal("unsafe prompt answered", prompt)
        }
    }
}

func TestCloudSessionRefreshesAndStripsPlatformTokens(t *testing.T) {
    t.Setenv("TEST_CLOUD_TOKEN", "platform-token")
    t.Setenv("OTHER_CLOUD_TOKEN", "other-token")
    b := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "pro", Account: "demo", ID: "pro-saved"}, Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "TEST_CLOUD_TOKEN"}}}
    target := config.Target{Cloud: &b, RemoteBase: "~/projects"}
    calls := 0
    client := &Client{Binary: "ssh", SensitiveEnv: []string{"OTHER_CLOUD_TOKEN"}, Resolve: func(_ context.Context, target config.Target) (config.Target, string, error) {
        calls++
        if target.Cloud.Ref.ID != "pro-saved" {
            t.Fatal(target)
        }
        target.Cloud = nil
        target.Destination = "root@fresh.example.com"
        target.Args = []string{"-p", "2222"}
        return target, "ssh-secret", nil
    }}
    session, err := client.prepare(context.Background(), target, true)
    if err != nil {
        t.Fatal(err)
    }
    defer session.close()
    if calls != 1 || session.batch || session.target.Destination != "root@fresh.example.com" || target.Cloud == nil || target.Destination != "" {
        t.Fatal(session, target)
    }
    for _, entry := range session.env {
        if strings.HasPrefix(entry, "TEST_CLOUD_TOKEN=") || strings.HasPrefix(entry, "OTHER_CLOUD_TOKEN=") || strings.Contains(entry, "ssh-secret") {
            t.Fatal("credential in SSH environment")
        }
    }
    args := strings.Join(arguments(session.target, "true", session.batch), " ")
    if !strings.Contains(args, "StrictHostKeyChecking=yes") || strings.Contains(args, "BatchMode=yes") || !strings.Contains(args, "ControlPath=none") {
        t.Fatal(args)
    }
}
