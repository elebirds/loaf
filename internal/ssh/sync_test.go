package ssh

import (
    "bytes"
    "context"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

func TestNativeRsyncUsesLiteralSSHAndReceiverArguments(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("POSIX local receiver fixture; Windows native transfer is covered by SSH integration")
    }
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Skip("system rsync is unavailable")
    }
    for _, destination := range []string{"fixture", "researcher@fixture"} {
        t.Run(destination, func(t *testing.T) {
            dir := t.TempDir()
            root := filepath.Join(dir, `source 中文 'single' "double"`)
            remote := filepath.Join(dir, `remote 中文 'single' "double" $literal`)
            if err := os.MkdirAll(root, 0700); err != nil {
                t.Fatal(err)
            }
            if err := os.WriteFile(filepath.Join(root, "train.py"), []byte("working source"), 0600); err != nil {
                t.Fatal(err)
            }
            receiver := filepath.Join(dir, `receiver 'single' "double"`)
            // A native rsync endpoint, with no Loaf header or stream relay.
            script := "#!/bin/sh\nset -eu\nmkdir -p \"$3/cache\"\ncd \"$3\"\nshift 4\nexec " + Quote(binary) + " \"$@\"\n"
            if err := os.WriteFile(receiver, []byte(script), 0700); err != nil {
                t.Fatal(err)
            }
            captured := filepath.Join(dir, "ssh-argv")
            fakeSSH := filepath.Join(dir, `ssh 'single' "double"`)
            script = "#!/bin/sh\nset -eu\nprintf '%s\\000' \"$@\" >" + Quote(captured) + "\nwhile [ \"$1\" != -- ]; do shift; done\nshift 2\nexec /bin/sh -c \"$*\"\n"
            if err := os.WriteFile(fakeSSH, []byte(script), 0700); err != nil {
                t.Fatal(err)
            }
            r := Rsync{Client: &Client{Binary: fakeSSH}, Binary: binary}
            configPath := filepath.Join(dir, `SSH 中文 'single' "double"`)
            result, err := r.Sync(context.Background(), config.Target{Destination: destination, Args: []string{"-F", configPath}}, receiver, root, SyncInput{Root: remote, ProjectID: "project", Mode: "mirror", Paths: []string{"train.py"}}, true)
            if err != nil {
                var f *fault.Error
                errors.As(err, &f)
                t.Fatal(err, f)
            }
            data, err := os.ReadFile(filepath.Join(remote, "cache", "train.py"))
            if err != nil || string(data) != "working source" || result.ChangedFiles != 1 {
                t.Fatal(string(data), result, err)
            }
            argv, err := os.ReadFile(captured)
            if err != nil || !bytes.Contains(argv, append([]byte(configPath), 0)) {
                t.Fatal(string(argv), err)
            }
        })
    }
}

func TestNativeRsyncFiltersMirrorOnlySelectedWorkingFiles(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("POSIX local receiver fixture; Windows native transfer is covered by SSH integration")
    }
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Skip("system rsync is unavailable")
    }
    root, destination := t.TempDir(), t.TempDir()
    names := []string{"#hash", ";semi", "line\nbreak", "中文 space", "literal[1]*?", "back\\slash", "back\\slash[1]", "]literal", "src/main.py", "now-file/old.py", "now-dir"}
    for _, name := range append(append([]string(nil), names...), "secret", "src/excluded") {
        file := filepath.Join(root, name)
        if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(file, []byte(name), 0600); err != nil {
            t.Fatal(err)
        }
    }
    if err := os.WriteFile(filepath.Join(destination, "obsolete"), []byte("old"), 0600); err != nil {
        t.Fatal(err)
    }
    transfer := func(paths []string, mirror bool) {
        t.Helper()
        filters, err := sourceFilters(paths)
        if err != nil {
            t.Fatal(err)
        }
        args := []string{"-rlptc", "--safe-links", "--from0", "--include-from=-", "--exclude=*"}
        if mirror {
            args = append(args, "--delete", "--delete-excluded")
        }
        args = append(args, "--", root+"/", destination+"/")
        cmd := exec.Command(binary, args...)
        cmd.Stdin = strings.NewReader(filters)
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatal(err, string(out))
        }
    }
    transfer(names, true)
    for _, name := range names {
        data, err := os.ReadFile(filepath.Join(destination, name))
        if err != nil || string(data) != name {
            t.Fatal(name, string(data), err)
        }
    }
    for _, name := range []string{"secret", "src/excluded", "obsolete"} {
        if _, err := os.Stat(filepath.Join(destination, name)); !errors.Is(err, os.ErrNotExist) {
            t.Fatal(name, err)
        }
    }
    if err := os.RemoveAll(filepath.Join(root, "now-file")); err != nil {
        t.Fatal(err)
    }
    if err := os.Remove(filepath.Join(root, "now-dir")); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(root, "now-file"), []byte("file"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := os.Mkdir(filepath.Join(root, "now-dir"), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(root, "now-dir/new.py"), []byte("directory"), 0600); err != nil {
        t.Fatal(err)
    }
    transfer([]string{"now-file", "now-dir/new.py"}, true)
    if data, err := os.ReadFile(filepath.Join(destination, "now-file")); err != nil || string(data) != "file" {
        t.Fatal(string(data), err)
    }
    if data, err := os.ReadFile(filepath.Join(destination, "now-dir/new.py")); err != nil || string(data) != "directory" {
        t.Fatal(string(data), err)
    }
    transfer(names[:len(names)-2], true)
    transfer([]string{"src/main.py"}, false)
    if _, err := os.Stat(filepath.Join(destination, "#hash")); err != nil {
        t.Fatal("update deleted an unselected path", err)
    }
    transfer([]string{"src/main.py"}, true)
    if _, err := os.Stat(filepath.Join(destination, "#hash")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("mirror retained an unselected path", err)
    }
    transfer(nil, true)
    entries, err := os.ReadDir(destination)
    if err != nil || len(entries) != 0 {
        t.Fatal("empty selection did not converge", entries, err)
    }
    for _, name := range []string{".", "../escape", "/absolute", "bad\x00name"} {
        if _, err := sourceFilters([]string{name}); err == nil {
            t.Fatal("accepted unsafe source", name)
        }
    }
}

func TestNativeSSHOptionsPreserveURLAndQuotedPaths(t *testing.T) {
    r := Rsync{Client: &Client{Binary: "/tools/ssh 'quoted'"}}
    shell, destination := r.remoteShell(config.Target{Destination: "ssh://user@[::1]:2222", Args: []string{"-F", "/private/中文 config"}}, true)
    for _, part := range []string{quoteRsync(r.Client.Binary), quoteRsync("/private/中文 config"), quoteRsync("StrictHostKeyChecking=yes"), quoteRsync("BatchMode=yes"), quoteRsync("2222"), quoteRsync("user")} {
        if !strings.Contains(shell, part) {
            t.Fatal(shell, part)
        }
    }
    if destination != "[::1]" || strings.Contains(shell, "__rsync") {
        t.Fatal(shell, destination)
    }
}

func TestRsyncUserOptionsPrecedeOptionTerminator(t *testing.T) {
    for _, tc := range []struct {
        destination, host, user string
    }{
        {"fixture", "fixture", ""},
        {"researcher@fixture", "fixture", "researcher"},
        {"researcher@127.0.0.1", "127.0.0.1", "researcher"},
        {"researcher@[::1]", "[::1]", "researcher"},
        {"ssh://researcher@[::1]", "[::1]", "researcher"},
    } {
        t.Run(tc.destination, func(t *testing.T) {
            // Explicit SSH options keep their earlier precedence over defaults
            // extracted from user@host, as with an ordinary SSH invocation.
            target := config.Target{Destination: tc.destination, Args: []string{"-l", "explicit"}}
            r := Rsync{Client: &Client{Binary: "ssh"}}
            shell, host := r.remoteShell(target, true)
            if tc.user != "" {
                target.Args = append(target.Args, "-l", tc.user)
            }
            argv := append([]string{"ssh"}, options(target, true)...)
            argv = append(argv, "--")
            for i := range argv {
                argv[i] = quoteRsync(argv[i])
            }
            if host != tc.host || shell != strings.Join(argv, " ") {
                t.Fatalf("remote shell = %q, host = %q; want %q, %q", shell, host, strings.Join(argv, " "), tc.host)
            }
        })
    }
}

func TestTransferFailureUsesOwnedCodesNotEnglishDiagnostics(t *testing.T) {
    for _, tc := range []struct{ diagnostic, code, message string }{
        {"rsync: permission denied", "SYNC_FAILED", "sync_failed"},
        {"LOAF_SYNC_ERROR:PROJECT_BUSY\n", "PROJECT_BUSY", "sync_busy"},
        {"LOAF_SYNC_ERROR:PROJECT_UNOWNED\n", "PROJECT_UNOWNED", "sync_unowned"},
        {"LOAF_SYNC_ERROR:BAD_RECEIVER\n", "BAD_RECEIVER", "sync_version"},
    } {
        var f *fault.Error
        if !errors.As(syncFailure(errors.New("exit 1"), tc.diagnostic), &f) || f.Code != tc.code || f.Message != tc.message || f.Params["Detail"] != strings.TrimSpace(tc.diagnostic) {
            t.Fatal(f)
        }
    }
    _, err := (Rsync{Client: &Client{Binary: "ssh"}}).Sync(context.Background(), config.Target{Destination: "host"}, "runner", t.TempDir(), SyncInput{}, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Message != "sync_rsync_missing" {
        t.Fatal(err)
    }
}
