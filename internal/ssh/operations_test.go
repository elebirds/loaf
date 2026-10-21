package ssh

import (
    "bytes"
    "context"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "reflect"
    "strconv"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func TestForegroundCommandPreservesArgumentsAndHome(t *testing.T) {
    home := t.TempDir()
    directory := filepath.Join(home, "work 中文 ' quote")
    if err := os.Mkdir(directory, 0700); err != nil {
        t.Fatal(err)
    }
    for _, cwd := range []string{"", "~", directory, "~/work 中文 ' quote", "work 中文 ' quote"} {
        args := []string{"/bin/sh", "-c", `printf '%s\000' "$PWD" "$@"`, "argument-zero", "a b", "quote'and\"double", "$(touch no);echo no", "--json", "", "line\nnext", "中文"}
        command, err := execCommand(args, cwd)
        if err != nil {
            t.Fatal(err)
        }
        cmd := exec.Command("/bin/sh", "-c", command)
        cmd.Env = append(os.Environ(), "HOME="+home, "CDPATH=/")
        cmd.Dir = "/"
        data, err := cmd.Output()
        if err != nil {
            t.Fatalf("cwd %q: %v", cwd, err)
        }
        wantDir := directory
        if cwd == "" || cwd == "~" {
            wantDir = home
        }
        // macOS resolves /var through /private/var when entering the directory.
        wantDir, err = filepath.EvalSymlinks(wantDir)
        if err != nil {
            t.Fatal(err)
        }
        want := append([]string{wantDir}, args[4:]...)
        got := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
        got[0], err = filepath.EvalSymlinks(got[0])
        if err != nil {
            t.Fatal(err)
        }
        if !reflect.DeepEqual(got, want) {
            t.Fatalf("cwd %q: %q, want %q", cwd, got, want)
        }
    }
    for _, tc := range []struct {
        args []string
        cwd  string
    }{{nil, ""}, {[]string{""}, ""}, {[]string{"printf", "a\x00b"}, ""}, {[]string{"pwd"}, "a\x00b"}} {
        if _, err := execCommand(tc.args, tc.cwd); err == nil {
            t.Fatal("invalid command accepted", tc)
        }
    }
}

func TestForegroundExitConfirmation(t *testing.T) {
    for _, tc := range []struct {
        status, code int
        want         string
    }{{0, 0, ""}, {7, 7, "EXEC_FAILED"}, {254, 254, "EXEC_FAILED"}, {255, 0, "EXEC_UNCONFIRMED"}} {
        binary := filepath.Join(t.TempDir(), "ssh")
        if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'out'\nprintf 'err' >&2\nexit "+strconv.Itoa(tc.status)+"\n"), 0700); err != nil {
            t.Fatal(err)
        }
        var stdout, stderr bytes.Buffer
        result, err := (&Client{Binary: binary}).Exec(context.Background(), config.Target{Destination: "host"}, ExecInput{Args: []string{"true"}, Batch: true}, nil, &stdout, &stderr)
        var f *fault.Error
        if tc.want == "" && err != nil || tc.want != "" && (!errors.As(err, &f) || f.Code != tc.want) {
            t.Fatalf("%d: %+v %v", tc.status, result, err)
        }
        if stdout.String() != "out" || stderr.String() != "err" {
            t.Fatal("streams were not preserved")
        }
        if tc.status == 255 {
            if result.Status != "unconfirmed" || result.ExitCode != nil {
                t.Fatal(result)
            }
        } else if result.ExitCode == nil || *result.ExitCode != tc.code {
            t.Fatal(result)
        }
    }
    result, err := (&Client{Binary: filepath.Join(t.TempDir(), "missing")}).Exec(context.Background(), config.Target{Destination: "host"}, ExecInput{Args: []string{"true"}}, nil, nil, nil)
    var f *fault.Error
    if result.Status != "not_started" || !errors.As(err, &f) || f.Code != "SSH_FAILED" {
        t.Fatal(result, err)
    }
}

func TestForwardNeverUsesPersistentControlConnection(t *testing.T) {
    args := forwardArguments(config.Target{Destination: "gpu", Args: []string{"-F", "a spaced config", "-J", "jump"}}, 16006, 6006, true)
    for _, option := range []string{"ControlMaster=no", "ControlPath=none", "ControlPersist=no", "ForkAfterAuthentication=no", "ExitOnForwardFailure=yes", "BatchMode=yes", "StrictHostKeyChecking=yes"} {
        if !containsArgument(args, option) {
            t.Fatal("missing option", option, args)
        }
    }
    if !containsArgument(args, "127.0.0.1:16006:127.0.0.1:6006") || !containsArgument(args, "a spaced config") || !containsArgument(args, "jump") {
        t.Fatal(args)
    }
    if strings.Contains(strings.Join(args, " "), "-f ") || strings.Contains(strings.Join(args, " "), "-O ") {
        t.Fatal("forward must not detach or control another session", args)
    }
}

func containsArgument(args []string, value string) bool {
    for _, arg := range args {
        if arg == value {
            return true
        }
    }
    return false
}
