package filelock

import (
    "bufio"
    "context"
    "errors"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/privatefs"
)

func TestLockProcessHelper(t *testing.T) {
    if os.Getenv("LOAF_TEST_LOCK_HELPER") != "1" {
        return
    }
    file, err := Acquire(context.Background(), os.Args[len(os.Args)-1], 0)
    if err != nil {
        t.Fatal(err)
    }
    defer file.Close()
    os.Stdout.WriteString("locked\n")
    io.Copy(io.Discard, os.Stdin)
}

func TestCrossProcessLockCancellationAndOwnerDeath(t *testing.T) {
    directory := filepath.Join(t.TempDir(), "private")
    if err := privatefs.MkdirAll(directory); err != nil {
        t.Fatal(err)
    }
    path := filepath.Join(directory, "resource.lock")
    cmd := exec.Command(os.Args[0], "-test.run=^TestLockProcessHelper$", "--", path)
    cmd.Env = append(os.Environ(), "LOAF_TEST_LOCK_HELPER=1")
    input, err := cmd.StdinPipe()
    if err != nil {
        t.Fatal(err)
    }
    defer input.Close()
    output, err := cmd.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    cmd.Stderr = os.Stderr
    if err := cmd.Start(); err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
    ready, err := bufio.NewReader(output).ReadString('\n')
    if err != nil || ready != "locked\n" {
        t.Fatal(ready, err)
    }
    if file, err := Acquire(context.Background(), path, 0); !errors.Is(err, ErrBusy) {
        if file != nil {
            file.Close()
        }
        t.Fatal("concurrent lock admitted", err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()
    if _, err := Acquire(ctx, path, -1); !errors.Is(err, context.DeadlineExceeded) {
        t.Fatal("lock wait ignored cancellation", err)
    }
    if err := cmd.Process.Kill(); err != nil {
        t.Fatal(err)
    }
    cmd.Wait()
    file, err := Acquire(context.Background(), path, time.Second)
    if err != nil {
        t.Fatal("dead process retained lock", err)
    }
    file.Close()
}
