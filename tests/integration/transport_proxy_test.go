package integration

import (
    "bytes"
    "encoding/json"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/ssh"
)

// The test executable doubles as a transparent transport fault injector. This
// keeps Unix shells out of local fixtures, without any production test hooks.
type transportFault struct {
    Tools      ssh.Tools
    Bandwidth  string
    DropAction string
    DropRead   string
    Marker     string
    Counter    string
}

func TestMain(m *testing.M) {
    executable, err := os.Executable()
    if err == nil {
        data, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "loaf-test-transport.json"))
        if err == nil {
            var fault transportFault
            if json.Unmarshal(data, &fault) != nil {
                os.Exit(125)
            }
            os.Exit(proxyTransport(filepath.Base(executable), fault))
        }
    }
    os.Exit(m.Run())
}

func proxyTransport(name string, fault transportFault) int {
    args := os.Args[1:]
    tool := fault.Tools.SSH
    stdin := io.Reader(os.Stdin)
    stdout := io.Writer(os.Stdout)
    drop := false
    switch strings.TrimSuffix(name, ".exe") {
    case "cygpath":
        tool = fault.Tools.Cygpath
    case "rsync":
        tool = fault.Tools.Rsync
        executable, _ := os.Executable()
        proxySSH := filepath.Join(filepath.Dir(executable), "ssh"+filepath.Ext(executable))
        // Fault injection targets finite Loaf control requests. Keep rsync's
        // native SSH stream entirely within its paired MSYS/POSIX tools.
        for i := 1; i < len(args); i++ {
            if args[i-1] == "-e" {
                args[i] = strings.ReplaceAll(args[i], filepath.ToSlash(proxySSH), filepath.ToSlash(fault.Tools.SSH))
            }
        }
        if fault.Bandwidth != "" && len(args) > 1 {
            args = append([]string{"--bwlimit=" + fault.Bandwidth}, args...)
        }
    case "ssh":
        // Only a fixed runner invocation carries a finite JSON request. Never
        // consume the rsync stream, upload script, master tether or terminal.
        command := ""
        if len(args) > 0 {
            command = args[len(args)-1]
        }
        if strings.HasSuffix(strings.Trim(command, "'\""), "/loaf-runner") && (fault.DropAction != "" || fault.DropRead != "") {
            payload, err := io.ReadAll(os.Stdin)
            if err != nil {
                return 125
            }
            stdin = bytes.NewReader(payload)
            var request struct {
                Action string `json:"action"`
            }
            if json.Unmarshal(payload, &request) == nil {
                drop = request.Action == fault.DropAction
                if drop && fault.Counter != "" {
                    file, err := os.OpenFile(fault.Counter, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
                    if err != nil {
                        return 125
                    }
                    _, err = file.WriteString(request.Action + "\n")
                    file.Close()
                    if err != nil {
                        return 125
                    }
                }
                if request.Action == fault.DropRead {
                    _, err := os.Stat(fault.Marker)
                    drop = drop || err == nil
                }
                if drop {
                    stdout = io.Discard
                }
            }
        }
    default:
        return 125
    }
    cmd := exec.Command(tool, args...)
    cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, os.Stderr
    if runtime.GOOS == "windows" {
        // Bridge through ordinary Win32 pipes when a native Go helper sits
        // between MSYS processes; MSYS's inherited descriptor table belongs
        // to its immediate parent and cannot be passed through a Go process.
        cmd.Stdin = struct{ io.Reader }{stdin}
        cmd.Stdout = struct{ io.Writer }{stdout}
        cmd.Stderr = struct{ io.Writer }{os.Stderr}
        cmd.WaitDelay = time.Second
    }
    err := cmd.Run()
    if drop {
        return 255
    }
    if err == nil || err == exec.ErrWaitDelay {
        return 0
    }
    if exit, ok := err.(*exec.ExitError); ok {
        return exit.ExitCode()
    }
    return 125
}

func transportProxy(t *testing.T, fault transportFault) (ssh.Tools, string) {
    t.Helper()
    tools, err := ssh.DiscoverTools()
    if err != nil {
        t.Fatal(err)
    }
    fault.Tools = tools
    root := t.TempDir()
    bin := filepath.Join(root, "usr", "bin")
    if err := os.MkdirAll(bin, 0700); err != nil {
        t.Fatal(err)
    }
    executable, err := os.Executable()
    if err != nil {
        t.Fatal(err)
    }
    suffix := ""
    if runtime.GOOS == "windows" {
        suffix = ".exe"
    }
    result := ssh.Tools{SSH: filepath.Join(bin, "ssh"+suffix), Rsync: filepath.Join(bin, "rsync"+suffix), Cygpath: filepath.Join(bin, "cygpath"+suffix)}
    for _, file := range []string{result.SSH, result.Rsync, result.Cygpath} {
        if err := os.Link(executable, file); err != nil {
            data, err := os.ReadFile(executable)
            if err != nil {
                t.Fatal(err)
            }
            if err := os.WriteFile(file, data, 0700); err != nil {
                t.Fatal(err)
            }
        }
    }
    // Native discovery still enforces a single, explicitly selected test root.
    for _, file := range []string{filepath.Join(root, "msys2_shell.cmd"), filepath.Join(bin, "msys-2.0.dll")} {
        if err := os.WriteFile(file, nil, 0600); err != nil {
            t.Fatal(err)
        }
    }
    data, _ := json.Marshal(fault)
    if err := os.WriteFile(filepath.Join(bin, "loaf-test-transport.json"), data, 0600); err != nil {
        t.Fatal(err)
    }
    return result, root
}

func proxyEnvironment(env []string, tools ssh.Tools, root string) []string {
    result := []string{}
    for _, value := range env {
        key, _, _ := strings.Cut(value, "=")
        if !strings.EqualFold(key, "PATH") && !strings.EqualFold(key, "LOAF_MSYS2_ROOT") {
            result = append(result, value)
        }
    }
    return append(result, "PATH="+filepath.Dir(tools.SSH)+string(os.PathListSeparator)+os.Getenv("PATH"), "LOAF_MSYS2_ROOT="+root)
}

func useTransportProxy(t *testing.T, fault transportFault) {
    t.Helper()
    tools, root := transportProxy(t, fault)
    t.Setenv("PATH", filepath.Dir(tools.SSH)+string(os.PathListSeparator)+os.Getenv("PATH"))
    t.Setenv("LOAF_MSYS2_ROOT", root)
}
