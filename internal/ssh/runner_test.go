package ssh

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/wire"
)

func TestInterruptedUploadDoesNotPublishRunner(t *testing.T) {
    home := t.TempDir()
    t.Setenv("HOME", home)
    fake := filepath.Join(t.TempDir(), "ssh")
    // Forward only eight bytes to the real remote upload script, then close input.
    script := []byte("#!/bin/sh\nfor argument do command=$argument; done\nhead -c 8 | /bin/sh -c \"$command\"\n")
    if err := os.WriteFile(fake, script, 0700); err != nil {
        t.Fatal(err)
    }
    c := &Client{Binary: fake}
    _, err := c.DeployRunner(context.Background(), config.Target{Destination: "fixture"}, wire.Version, bytes.Repeat([]byte("x"), 1<<16), true)
    if err == nil {
        t.Fatal("partial upload accepted")
    }
    dir := filepath.Join(home, ".local/share/loaf/bin", wire.Version)
    if _, err := os.Stat(filepath.Join(dir, "loaf-runner")); !os.IsNotExist(err) {
        t.Fatal("published incomplete executable", err)
    }
    remaining, err := filepath.Glob(filepath.Join(dir, ".upload.*"))
    if err != nil || len(remaining) != 0 {
        t.Fatal("upload temporary file leaked", remaining, err)
    }
}

func TestRunnerVersionMismatchUpdatesAtomically(t *testing.T) {
    home := t.TempDir()
    t.Setenv("HOME", home)
    fake := filepath.Join(t.TempDir(), "ssh")
    if err := os.WriteFile(fake, []byte("#!/bin/sh\nfor argument do command=$argument; done\nexec /bin/sh -c \"$command\"\n"), 0700); err != nil {
        t.Fatal(err)
    }
    target := filepath.Join(home, ".local/share/loaf/bin", wire.Version, "loaf-runner")
    if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
        t.Fatal(err)
    }
    runner := func(version string) []byte {
        response, err := json.Marshal(wire.Response{FormatVersion: wire.FormatVersion, Version: version, OK: true})
        if err != nil {
            t.Fatal(err)
        }
        return []byte(fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf '%%s' %s\n", Quote(string(response))))
    }
    old := runner("mismatched-version")
    if err := os.WriteFile(target, old, 0700); err != nil {
        t.Fatal(err)
    }
    // A process already using the previous inode must not see it overwritten.
    opened, err := os.Open(target)
    if err != nil {
        t.Fatal(err)
    }
    defer opened.Close()
    current := runner(wire.Version)
    client := &Client{Binary: fake}
    got, err := client.DeployRunner(context.Background(), config.Target{Destination: "fixture"}, wire.Version, current, true)
    data, readErr := os.ReadFile(target)
    retained := make([]byte, len(old))
    n, retainedErr := opened.Read(retained)
    if err != nil || got != target || readErr != nil || !bytes.Equal(data, current) || retainedErr != nil || n != len(old) || !bytes.Equal(retained, old) {
        t.Fatal(got, err, string(data), readErr, string(retained), retainedErr)
    }
    // A matching executable is left alone, not republished on every query.
    candidate := append(append([]byte(nil), current...), []byte("# unnecessary replacement\n")...)
    if _, err := client.DeployRunner(context.Background(), config.Target{Destination: "fixture"}, wire.Version, candidate, true); err != nil {
        t.Fatal(err)
    }
    if data, err := os.ReadFile(target); err != nil || !bytes.Equal(data, current) {
        t.Fatal("matching runner was replaced", string(data), err)
    }
    // An incompatible version handshake is replaced, never adapted in place.
    invalid := []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"format_version\":99,\"version\":\"different\",\"ok\":true}'\n")
    if err := os.WriteFile(target, invalid, 0700); err != nil {
        t.Fatal(err)
    }
    if _, err := client.DeployRunner(context.Background(), config.Target{Destination: "fixture"}, wire.Version, current, true); err != nil {
        t.Fatal("incompatible handshake was not replaced", err)
    }
}

func TestControlRejectsDifferentClientVersionBeforeSSH(t *testing.T) {
    _, err := (&Client{Binary: "not-installed"}).Control(context.Background(), config.Target{}, "/runner", wire.Request{Version: "another-version"}, true)
    if err == nil || err.Error() != "VERSION_MISMATCH" {
        t.Fatal(err)
    }
}
