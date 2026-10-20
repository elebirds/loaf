package ssh

import (
    "bytes"
    "context"
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/config"
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
    _, err := c.DeployRunner(context.Background(), config.Target{Destination: "fixture"}, "interrupted", bytes.Repeat([]byte("x"), 1<<16), true)
    if err == nil {
        t.Fatal("partial upload accepted")
    }
    dir := filepath.Join(home, ".local/share/loaf/bin/interrupted")
    if _, err := os.Stat(filepath.Join(dir, "loaf-runner")); !os.IsNotExist(err) {
        t.Fatal("published incomplete executable", err)
    }
    remaining, err := filepath.Glob(filepath.Join(dir, ".upload.*"))
    if err != nil || len(remaining) != 0 {
        t.Fatal("upload temporary file leaked", remaining, err)
    }
}
