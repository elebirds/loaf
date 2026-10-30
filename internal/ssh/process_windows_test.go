package ssh

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "os"
    "os/exec"
    "strconv"
    "strings"
    "syscall"
    "testing"
    "time"

    "golang.org/x/sys/windows"
)

func TestWindowsProcessHelper(t *testing.T) {
    mode := os.Getenv("LOAF_PROCESS_TEST")
    if mode == "" {
        return
    }
    if mode == "args" {
        json.NewEncoder(os.Stdout).Encode(os.Args[3:])
        os.Exit(0)
    }
    if mode == "leaf" {
        fmt.Println(os.Getpid())
        time.Sleep(time.Minute)
        os.Exit(0)
    }
    if mode == "tree" {
        child := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessHelper$")
        child.Env = append(withoutEnv(os.Environ(), "LOAF_PROCESS_TEST"), "LOAF_PROCESS_TEST=leaf")
        child.Stdout, child.Stderr = os.Stdout, os.Stderr
        if err := child.Run(); err != nil {
            os.Exit(1)
        }
        os.Exit(0)
    }
    if mode == "owner" {
        child := commandContext(context.Background(), os.Args[0], "-test.run=^TestWindowsProcessHelper$")
        child.Env = append(withoutEnv(os.Environ(), "LOAF_PROCESS_TEST"), "LOAF_PROCESS_TEST=tree")
        child.Stdout, child.Stderr = os.Stdout, os.Stderr
        if err := child.Run(); err != nil {
            fmt.Fprintln(os.Stderr, err)
            os.Exit(1)
        }
        os.Exit(0)
    }
    if mode == "before-admission" {
        child := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestWindowsProcessHelper$")
        child.Env = append(withoutEnv(os.Environ(), "LOAF_PROCESS_TEST"), "LOAF_PROCESS_TEST=leaf")
        owner, err := ownProcess(child)
        if err != nil {
            t.Fatal(err)
        }
        defer owner.close()
        // Deliberately stop between CreateProcess and per-command admission.
        // The inherited lifetime job must already own this suspended process.
        if err := child.Start(); err != nil {
            t.Fatal(err)
        }
        fmt.Println(child.Process.Pid)
        time.Sleep(time.Minute)
        os.Exit(1)
    }
    os.Exit(2)
}

func TestWindowsProcessTreeOwnership(t *testing.T) {
    for _, mode := range []string{"cancel", "owner", "before-admission"} {
        t.Run(mode, func(t *testing.T) {
            killOwner := mode != "cancel"
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()
            var cmd interface {
                Start() error
                Wait() error
            }
            var native *exec.Cmd
            var output *bufio.Reader
            if killOwner {
                native = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsProcessHelper$")
                native.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
                native.Stderr = os.Stderr
                native.Env = append(os.Environ(), "LOAF_PROCESS_TEST="+mode)
                pipe, err := native.StdoutPipe()
                if err != nil {
                    t.Fatal(err)
                }
                defer pipe.Close()
                output, cmd = bufio.NewReader(pipe), native
            } else {
                owned := commandContext(ctx, os.Args[0], "-test.run=^TestWindowsProcessHelper$")
                owned.Env = append(os.Environ(), "LOAF_PROCESS_TEST=tree")
                pipe, err := owned.StdoutPipe()
                if err != nil {
                    t.Fatal(err)
                }
                defer pipe.Close()
                output, cmd = bufio.NewReader(pipe), owned
            }
            if err := cmd.Start(); err != nil {
                t.Fatal(err)
            }
            line, err := output.ReadString('\n')
            if err != nil {
                cancel()
                cmd.Wait()
                t.Fatal(err)
            }
            pid, err := strconv.Atoi(strings.TrimSpace(line))
            if err != nil {
                t.Fatal(err)
            }
            handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
            if err != nil {
                t.Fatal(err)
            }
            defer windows.CloseHandle(handle)
            if killOwner {
                native.Process.Kill()
            } else {
                cancel()
            }
            cmd.Wait()
            status, err := windows.WaitForSingleObject(handle, 5000)
            if err != nil || status != windows.WAIT_OBJECT_0 {
                t.Fatal("orphaned descendant", status, err)
            }
        })
    }
}

func TestWindowsProcessPreservesArguments(t *testing.T) {
    want := []string{"", "中文 空格", `a\b\`, `a\"b`, "$(unchanged)", "--", "x\ny"}
    args := append([]string{"-test.run=^TestWindowsProcessHelper$", "--"}, want...)
    cmd := commandContext(context.Background(), os.Args[0], args...)
    cmd.Env = append(os.Environ(), "LOAF_PROCESS_TEST=args")
    data, err := cmd.Output()
    if err != nil {
        t.Fatal(err)
    }
    var got []string
    if err := json.Unmarshal(data, &got); err != nil {
        t.Fatal(err, string(data))
    }
    if fmt.Sprint(got) != fmt.Sprint(want) {
        t.Fatalf("%q != %q", got, want)
    }
}
