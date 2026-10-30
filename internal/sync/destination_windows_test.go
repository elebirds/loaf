package sync

import (
    "bufio"
    "context"
    "errors"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/filelock"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/windows"
)

func TestWindowsDestinationOwnerDeath(t *testing.T) {
    if os.Getenv("LOAF_DESTINATION_LOCK_CHILD") == "1" {
        lock, err := lockDestination(os.Args[len(os.Args)-1])
        if err != nil {
            t.Fatal(err)
        }
        defer lock.Close()
        os.Stdout.WriteString("locked\n")
        io.Copy(io.Discard, os.Stdin)
        return
    }
    directory := t.TempDir()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsDestinationOwnerDeath$", "--", directory)
    cmd.Env = append(os.Environ(), "LOAF_DESTINATION_LOCK_CHILD=1")
    stdin, err := cmd.StdinPipe()
    if err != nil {
        t.Fatal(err)
    }
    defer stdin.Close()
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    if err := cmd.Start(); err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
    if text, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || text != "locked\n" {
        t.Fatal(text, err)
    }
    if lock, err := lockDestination(strings.ToUpper(directory)); !errors.Is(err, filelock.ErrBusy) {
        if lock != nil {
            lock.Close()
        }
        t.Fatal("cross-process alias bypassed destination lock", err)
    }
    if err := cmd.Process.Kill(); err != nil {
        t.Fatal(err)
    }
    cmd.Wait()
    lock, err := lockDestination(directory)
    if err != nil {
        t.Fatal("abandoned mutex was not recoverable", err)
    }
    lock.Close()
}

func TestWindowsDownloadLockUsesDirectoryIdentity(t *testing.T) {
    directory := filepath.Join(t.TempDir(), "下载 Space")
    if err := os.Mkdir(directory, 0700); err != nil {
        t.Fatal(err)
    }
    owner, err := lockDestination(directory)
    if err != nil {
        t.Fatal(err)
    }
    defer owner.Close()
    for _, alias := range []string{strings.ToUpper(directory), filepath.ToSlash(directory), directory + `\.`} {
        second, err := lockDestination(alias)
        if err == nil {
            second.Close()
            t.Fatal("destination alias bypassed lock", alias)
        }
        if !errors.Is(err, filelock.ErrBusy) {
            t.Fatal(err)
        }
    }
    owner.Close()
    next, err := lockDestination(strings.ToUpper(directory))
    if err != nil {
        t.Fatal("released destination cannot be reused", err)
    }
    next.Close()
}

func TestWindowsDownloadRejectsExistingShortNameAlias(t *testing.T) {
    directory := t.TempDir()
    original := filepath.Join(directory, "Long output filename.txt")
    if err := os.WriteFile(original, []byte("keep"), 0600); err != nil {
        t.Fatal(err)
    }
    name, err := windows.UTF16PtrFromString(original)
    if err != nil {
        t.Fatal(err)
    }
    buffer := make([]uint16, 32768)
    n, err := windows.GetShortPathName(name, &buffer[0], uint32(len(buffer)))
    if err != nil {
        t.Fatal(err)
    }
    alias := filepath.Base(windows.UTF16ToString(buffer[:n]))
    if alias == filepath.Base(original) {
        t.Skip("this volume has no 8.3 alias for the fixture file")
    }
    names := []wire.OutputName{{Path: alias, Kind: "file"}}
    selected := wire.OutputSelection{Available: []string{alias}, Names: names, Namespace: wire.OutputNamespace(names)}
    if err := validateDestinationNamespace(directory, selected, &wire.OutputFilesystem{Name: "windows-ntfs"}); err == nil {
        t.Fatal("8.3 alias could overwrite a different long filename")
    }
    if data, err := os.ReadFile(original); err != nil || string(data) != "keep" {
        t.Fatal(err)
    }
}

func TestWindowsDownloadNameValidationPreservesLocalFiles(t *testing.T) {
    dir := t.TempDir()
    if err := os.WriteFile(filepath.Join(dir, "Result.txt"), []byte("keep"), 0600); err != nil {
        t.Fatal(err)
    }
    for _, names := range [][]wire.OutputName{
        {{Path: "result.txt", Kind: "file"}},
        {{Path: "new/A", Kind: "file"}, {Path: "new/a", Kind: "file"}},
        {{Path: "new/NUL.txt", Kind: "file"}},
        {{Path: "new/data:stream", Kind: "file"}},
        {{Path: "new/link", Kind: "symlink"}},
    } {
        selection := wire.OutputSelection{Available: []string{names[0].Path}, Names: names, Namespace: wire.OutputNamespace(names)}
        if err := validateDestinationNamespace(dir, selection, &wire.OutputFilesystem{Name: "windows-ntfs"}); err == nil {
            t.Fatal("unsafe names accepted", names)
        }
    }
    data, err := os.ReadFile(filepath.Join(dir, "Result.txt"))
    if err != nil || string(data) != "keep" {
        t.Fatal("existing file changed", err)
    }
    for _, name := range []string{`..\escape`, `C:\absolute`, "trailing.", "LPT¹.log"} {
        if _, err := openDestination(filepath.Join(dir, "uncreated"), []string{name}); err == nil {
            t.Fatal("unsafe destination created", name)
        }
    }
    if _, err := os.Stat(filepath.Join(dir, "uncreated")); !os.IsNotExist(err) {
        t.Fatal("invalid paths wrote a destination", err)
    }
}
