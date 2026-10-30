package interaction

import (
    "context"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
    "unicode/utf16"
    "unsafe"

    "golang.org/x/sys/windows"
)

// This fixture owns a hidden ConPTY. It never injects input into a user's
// console, and checks the actual Windows console APIs rather than a pipe mock.
func TestWindowsConsoleSecret(t *testing.T) {
    if len(os.Args) >= 3 && os.Args[len(os.Args)-3] == "loaf-console-child" {
        consoleSecretChild(t, os.Args[len(os.Args)-2], os.Args[len(os.Args)-1])
        return
    }
    for _, mode := range []string{"success", "cancel"} {
        t.Run(mode, func(t *testing.T) {
            dir := t.TempDir()
            input, output := consoleChild(t, mode, dir)
            text := ""
            until := func(marker string) {
                t.Helper()
                timer := time.NewTimer(5 * time.Second)
                defer timer.Stop()
                for !strings.Contains(text, marker) {
                    select {
                    case chunk, open := <-output:
                        if !open {
                            t.Fatalf("console exited before %s: %s", marker, text)
                        }
                        text += chunk
                    case <-timer.C:
                        t.Fatalf("console did not reach %s: %s", marker, text)
                    }
                }
            }
            until("HIDDEN")
            const secret = "密码 中文 🔑 private"
            if mode == "success" {
                if _, err := input.WriteString(secret + "\r"); err != nil {
                    t.Fatal(err)
                }
            }
            if mode == "cancel" {
                if err := os.WriteFile(filepath.Join(dir, "cancel"), nil, 0600); err != nil {
                    t.Fatal(err)
                }
                until("NEXT")
                if _, err := input.WriteString("next 中文\r"); err != nil {
                    t.Fatal(err)
                }
            }
            until("DONE")
            if strings.Contains(text, secret) || strings.Contains(text, "private") || strings.Contains(text, "密码") {
                t.Fatal("console echoed secret input")
            }
        })
    }
}

func consoleSecretChild(t *testing.T, mode, dir string) {
    handle := windows.Handle(os.Stdin.Fd())
    var before uint32
    if err := windows.GetConsoleMode(handle, &before); err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    type result struct {
        value []byte
        err   error
    }
    done := make(chan result, 1)
    go func() { value, err := ReadTerminalSecret(ctx, os.Stdin, 4096); done <- result{value, err} }()
    deadline := time.Now().Add(3 * time.Second)
    for {
        var current uint32
        if err := windows.GetConsoleMode(handle, &current); err != nil {
            t.Fatal(err)
        }
        if current&windows.ENABLE_ECHO_INPUT == 0 {
            break
        }
        if time.Now().After(deadline) {
            t.Fatal("echo remained enabled")
        }
        time.Sleep(time.Millisecond)
    }
    fmt.Println("HIDDEN")
    if mode == "cancel" {
        for {
            if _, err := os.Stat(filepath.Join(dir, "cancel")); err == nil {
                break
            }
            if ctx.Err() != nil {
                t.Fatal(ctx.Err())
            }
            time.Sleep(time.Millisecond)
        }
        // Queue input directly in this owned console, then wait until the
        // pending cooked read consumes it. A ConPTY pipe write and a separate
        // cancellation signal otherwise race before terminal input delivery.
        type keyRecord struct {
            Type    uint16
            Padding uint16
            Down    int32
            Repeat  uint16
            Key     uint16
            Scan    uint16
            Char    uint16
            Control uint32
        }
        records := []keyRecord{}
        for _, unit := range utf16.Encode([]rune("密码 中文 🔑 private")) {
            records = append(records, keyRecord{Type: 1, Down: 1, Repeat: 1, Char: unit})
        }
        var written uint32
        kernel := windows.NewLazySystemDLL("kernel32.dll")
        ok, _, err := kernel.NewProc("WriteConsoleInputW").Call(uintptr(handle), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
        if ok == 0 || int(written) != len(records) {
            t.Fatal("queue console input", err)
        }
        for {
            var count uint32
            ok, _, err := kernel.NewProc("GetNumberOfConsoleInputEvents").Call(uintptr(handle), uintptr(unsafe.Pointer(&count)))
            if ok == 0 {
                t.Fatal(err)
            }
            if count == 0 {
                break
            }
            if ctx.Err() != nil {
                t.Fatal(ctx.Err())
            }
            time.Sleep(time.Millisecond)
        }
        cancel()
    }
    got := <-done
    defer clear(got.value)
    if mode == "cancel" {
        if !errors.Is(got.err, context.Canceled) || got.value != nil {
            t.Fatal("cancellation failed", got.err)
        }
    } else if got.err != nil || string(got.value) != "密码 中文 🔑 private" {
        t.Fatalf("synthetic secret bytes changed: %x (%v)", got.value, got.err)
    }
    var after uint32
    if err := windows.GetConsoleMode(handle, &after); err != nil || before != after {
        t.Fatal("console mode was not restored", before, after, err)
    }
    if mode == "cancel" {
        fmt.Println("NEXT")
        value, err := readLine(context.Background(), os.Stdin, 4096)
        defer clear(value)
        if err != nil || string(value) != "next 中文" {
            t.Fatalf("synthetic next input changed: %x (%v)", value, err)
        }
    }
    fmt.Println("DONE")
}

func consoleChild(t *testing.T, mode, dir string) (*os.File, <-chan string) {
    t.Helper()
    inputRead, inputWrite, err := os.Pipe()
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { inputRead.Close(); inputWrite.Close() })
    outputRead, outputWrite, err := os.Pipe()
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { outputRead.Close(); outputWrite.Close() })
    var console windows.Handle
    if err := windows.CreatePseudoConsole(windows.Coord{X: 100, Y: 24}, windows.Handle(inputRead.Fd()), windows.Handle(outputWrite.Fd()), 0, &console); err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { windows.ClosePseudoConsole(console) })
    inputRead.Close()
    outputWrite.Close()
    attributes, err := windows.NewProcThreadAttributeList(1)
    if err != nil {
        t.Fatal(err)
    }
    defer attributes.Delete()
    // PSEUDOCONSOLE is a handle value, unlike pointer-valued attributes.
    update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
    ok, _, updateErr := update.Call(uintptr(unsafe.Pointer(attributes.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0)
    if ok == 0 {
        t.Fatal(updateErr)
    }
    executable, err := os.Executable()
    if err != nil {
        t.Fatal(err)
    }
    application, err := windows.UTF16PtrFromString(executable)
    if err != nil {
        t.Fatal(err)
    }
    command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{executable, "-test.run=^TestWindowsConsoleSecret$", "--", "loaf-console-child", mode, dir}))
    if err != nil {
        t.Fatal(err)
    }
    startup := windows.StartupInfoEx{}
    startup.Flags = windows.STARTF_USESTDHANDLES
    startup.Cb = uint32(unsafe.Sizeof(startup))
    startup.ProcThreadAttributeList = attributes.List()
    var process windows.ProcessInformation
    if err := windows.CreateProcess(application, command, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &startup.StartupInfo, &process); err != nil {
        t.Fatal(err)
    }
    windows.CloseHandle(process.Thread)
    t.Cleanup(func() {
        if result, _ := windows.WaitForSingleObject(process.Process, 2000); result != windows.WAIT_OBJECT_0 {
            windows.TerminateProcess(process.Process, 1)
            t.Error("console child did not exit")
        }
        var code uint32
        windows.GetExitCodeProcess(process.Process, &code)
        windows.CloseHandle(process.Process)
        if code != 0 {
            t.Errorf("console child exit %d", code)
        }
    })
    chunks := make(chan string, 32)
    go func() {
        defer close(chunks)
        data := make([]byte, 4096)
        for {
            n, err := outputRead.Read(data)
            if n > 0 {
                chunks <- string(data[:n])
            }
            if err != nil {
                return
            }
        }
    }()
    return inputWrite, chunks
}
