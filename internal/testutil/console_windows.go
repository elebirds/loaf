package testutil

import (
    "os"
    "strings"
    "sync"
    "testing"
    "time"
    "unsafe"

    "golang.org/x/sys/windows"
)

// Console owns a hidden ConPTY and its child test process. Output is drained
// continuously, including while the parent is waiting for child exit.
type Console struct {
    Input *os.File
    mu    sync.Mutex
    text  strings.Builder
}

func (c *Console) Text() string {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.text.String()
}

func (c *Console) Wait(t testing.TB, marker string) {
    t.Helper()
    deadline := time.Now().Add(15 * time.Second)
    for !strings.Contains(c.Text(), marker) {
        if time.Now().After(deadline) {
            t.Fatalf("owned console did not reach %q: %s", marker, c.Text())
        }
        time.Sleep(5 * time.Millisecond)
    }
}

// ConsoleProcess executes this test binary in an isolated console, never in the
// user's terminal. Callers supply an exact child test selector and fixture args.
func ConsoleProcess(t testing.TB, columns, rows int16, args ...string) *Console {
    t.Helper()
    var drained <-chan struct{}
    t.Cleanup(func() {
        if drained != nil {
            <-drained
        }
    })
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
    if err := windows.CreatePseudoConsole(windows.Coord{X: columns, Y: rows}, windows.Handle(inputRead.Fd()), windows.Handle(outputWrite.Fd()), 0, &console); err != nil {
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
    command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{executable}, args...)))
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
        if result, _ := windows.WaitForSingleObject(process.Process, 5000); result != windows.WAIT_OBJECT_0 {
            windows.TerminateProcess(process.Process, 1)
            windows.WaitForSingleObject(process.Process, 5000)
            t.Error("console child did not exit")
        }
        var code uint32
        windows.GetExitCodeProcess(process.Process, &code)
        windows.CloseHandle(process.Process)
        if code != 0 {
            t.Errorf("console child exit %d", code)
        }
    })
    result := &Console{Input: inputWrite}
    done := make(chan struct{})
    drained = done
    go func() {
        defer close(done)
        data := make([]byte, 4096)
        for {
            n, err := outputRead.Read(data)
            if n > 0 {
                result.mu.Lock()
                if result.text.Len() < 1<<20 {
                    result.text.Write(data[:n])
                }
                result.mu.Unlock()
            }
            if err != nil {
                return
            }
        }
    }()
    return result
}
