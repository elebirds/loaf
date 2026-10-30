package integration

import (
    "fmt"
    "io"
    "os"
    "os/exec"
    "sort"
    "strings"
    "sync"
    "testing"
    "time"
    "unicode/utf16"
    "unsafe"

    "golang.org/x/sys/windows"
)

type consolePipe struct{ input, output *os.File }

func (p consolePipe) Read(b []byte) (int, error)  { return p.output.Read(b) }
func (p consolePipe) Write(b []byte) (int, error) { return p.input.Write(b) }
func (p consolePipe) Close() error                { p.input.Close(); return p.output.Close() }

// An owned, hidden ConPTY; no interaction with the user's terminal or desktop.
func startTerminal(t *testing.T, cmd *exec.Cmd) (io.ReadWriteCloser, func() error) {
    t.Helper()
    input, writer, err := os.Pipe()
    if err != nil {
        t.Fatal(err)
    }
    output, reader, err := os.Pipe()
    if err != nil {
        t.Fatal(err)
    }
    var console windows.Handle
    if err := windows.CreatePseudoConsole(windows.Coord{X: 120, Y: 40}, windows.Handle(input.Fd()), windows.Handle(reader.Fd()), 0, &console); err != nil {
        t.Fatal(err)
    }
    input.Close()
    reader.Close()
    streams := consolePipe{writer, output}
    attributes, err := windows.NewProcThreadAttributeList(1)
    if err != nil {
        t.Fatal(err)
    }
    defer attributes.Delete()
    update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
    ok, _, err := update.Call(uintptr(unsafe.Pointer(attributes.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0)
    if ok == 0 {
        t.Fatal(err)
    }
    app, err := windows.UTF16PtrFromString(cmd.Path)
    if err != nil {
        t.Fatal(err)
    }
    line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
    if err != nil {
        t.Fatal(err)
    }
    var dir *uint16
    if cmd.Dir != "" {
        dir, err = windows.UTF16PtrFromString(cmd.Dir)
        if err != nil {
            t.Fatal(err)
        }
    }
    env := cmd.Environ()
    sort.Slice(env, func(i, j int) bool { return strings.ToUpper(env[i]) < strings.ToUpper(env[j]) })
    block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
    startup := windows.StartupInfoEx{}
    startup.Cb = uint32(unsafe.Sizeof(startup))
    startup.Flags = windows.STARTF_USESTDHANDLES
    startup.ProcThreadAttributeList = attributes.List()
    var process windows.ProcessInformation
    if err := windows.CreateProcess(app, line, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, &block[0], dir, &startup.StartupInfo, &process); err != nil {
        t.Fatal(err)
    }
    windows.CloseHandle(process.Thread)
    done := make(chan struct{})
    watchdog := time.AfterFunc(30*time.Second, func() { windows.TerminateProcess(process.Process, 1) })
    var result error
    go func() {
        _, err := windows.WaitForSingleObject(process.Process, windows.INFINITE)
        var code uint32
        if err == nil {
            err = windows.GetExitCodeProcess(process.Process, &code)
        }
        if err != nil {
            result = err
        } else if code != 0 {
            result = fmt.Errorf("console child exit %d", code)
        }
        watchdog.Stop()
        windows.ClosePseudoConsole(console)
        close(done)
    }()
    var closeOnce sync.Once
    t.Cleanup(func() {
        closeOnce.Do(func() {
            streams.Close()
            select {
            case <-done:
            default:
                windows.TerminateProcess(process.Process, 1)
                <-done
            }
            windows.CloseHandle(process.Process)
        })
    })
    return streams, func() error { <-done; return result }
}
