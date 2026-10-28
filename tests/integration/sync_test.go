package integration

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/wire"
)

func sourceFile(t *testing.T, root, name, text string) {
    t.Helper()
    file := filepath.Join(root, name)
    if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(file, []byte(text), 0600); err != nil {
        t.Fatal(err)
    }
}

func TestRsyncWorkingSources(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    project := t.TempDir()
    for _, name := range []string{"train.py", "旧 源码.py", "#hash.py", ";semi.py", "line\nbreak.py", "literal[1]*?.py", "src/中文.py"} {
        sourceFile(t, project, name, "print(1)\n")
    }
    sourceFile(t, project, ".env", "SECRET=not-uploaded")
    sourceFile(t, project, ".venv/bin/python", "not-uploaded")
    sourceFile(t, project, "checkpoints/model.pt", "not-uploaded")
    sourceFile(t, project, "outputs/result.json", "not-uploaded")
    if err := os.Symlink("src/中文.py", filepath.Join(project, "linked.py")); err != nil {
        t.Fatal(err)
    }
    root := "/home/researcher/sync space 中文 'quote' ;literal"
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Fatal(err)
    }
    bootstrap := execution.Bootstrap{Remote: f.client}
    service := sources.Service{Check: f.client, Runner: func(ctx context.Context, target config.Target, arch string, batch bool) (string, error) {
        info, err := bootstrap.Verify(ctx, target, arch, batch)
        return info.Path, err
    }, Send: ssh.Rsync{Client: f.client, Binary: binary}}
    syncProject := func() sources.Result {
        t.Helper()
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        plan, err := sources.Select(ctx, project, sources.SelectOptions{})
        if err != nil {
            t.Fatal(err)
        }
        result, err := service.Sync(ctx, f.target, "sync-project", root, plan, true)
        if err != nil {
            var f *fault.Error
            if errors.As(err, &f) {
                t.Fatalf("sync failed: %v %+v", err, f.Params)
            }
            t.Fatalf("sync failed: %v", err)
        }
        return result
    }
    first := syncProject()
    f.shell(`test -f "$1/cache/train.py"; test -f "$1/cache/#hash.py"; test -f "$1/cache/;semi.py"; test -f "$1/cache/literal[1]*?.py"; test ! -e "$1/cache/.env"; test ! -e "$1/cache/.venv"; test ! -e "$1/cache/outputs"; test ! -e "$1/cache/checkpoints"; test "$(readlink "$1/cache/linked.py")" = 'src/中文.py'`, root)
    unchanged := syncProject()
    if first.ChangedFiles < 1 || unchanged.ChangedFiles != 0 {
        t.Fatal(first, unchanged)
    }
    f.shell(`printf survive >"$1/sentinel"; mkdir -p "$1/runs/old/work"; printf old >"$1/runs/old/work/train.py"`, root)
    // Same size and mtime catches rsync quick-check omissions, not just slow edits.
    info, err := os.Stat(filepath.Join(project, "train.py"))
    if err != nil {
        t.Fatal(err)
    }
    sourceFile(t, project, "train.py", "print(2)\n")
    if err := os.Chtimes(filepath.Join(project, "train.py"), info.ModTime(), info.ModTime()); err != nil {
        t.Fatal(err)
    }
    if err := os.Remove(filepath.Join(project, "旧 源码.py")); err != nil {
        t.Fatal(err)
    }
    changed := syncProject()
    if changed.ChangedFiles != 1 {
        t.Fatal(changed)
    }
    f.shell(`grep -q 'print(2)' "$1/cache/train.py"; test ! -e "$1/cache/旧 源码.py"; test "$(cat "$1/sentinel")" = survive; test "$(cat "$1/runs/old/work/train.py")" = old`, root)
    // Native rsync must replace a receiver directory link, not follow it.
    outside := "/home/researcher/outside-cache"
    f.shell(`mkdir -p "$2"; printf outside >"$2/sentinel"; rm -rf "$1/cache/src"; ln -s "$2" "$1/cache/src"`, root, outside)
    syncProject()
    f.shell(`test -d "$1/cache/src"; test ! -L "$1/cache/src"; test -f "$1/cache/src/中文.py"; test "$(cat "$2/sentinel")" = outside; test ! -e "$2/中文.py"`, root, outside)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    report, err := f.client.Check(ctx, f.target, root, true)
    if err != nil {
        t.Fatal(err)
    }
    runner, err := bootstrap.Verify(ctx, f.target, report.Arch, true)
    if err != nil {
        t.Fatal(err)
    }
    // Inspect the committed sync state through the real fixture database.
    if state := f.remoteSQL(root, "SELECT complete FROM sync_state WHERE singleton=1"); state != "1" {
        t.Fatal(state)
    }
    _, err = f.client.Control(ctx, f.target, runner.Path, wire.Request{FormatVersion: 1, Version: wire.Version, Action: "version"}, true)
    if err != nil {
        t.Fatal(err)
    }
    t.Logf("rsync selected-source changes: first=%d, unchanged=%d, same-size edit=%d", first.ChangedFiles, unchanged.ChangedFiles, changed.ChangedFiles)
}

func TestRealProjectSourceTree(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    root, err := filepath.Abs("../..")
    if err != nil {
        t.Fatal(err)
    }
    plan, err := sources.Select(context.Background(), root, sources.SelectOptions{})
    if err != nil {
        t.Fatal(err)
    }
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Fatal(err)
    }
    bootstrap := execution.Bootstrap{Remote: f.client}
    service := sources.Service{Check: f.client, Runner: func(ctx context.Context, target config.Target, arch string, batch bool) (string, error) {
        info, err := bootstrap.Verify(ctx, target, arch, batch)
        return info.Path, err
    }, Send: ssh.Rsync{Client: f.client, Binary: binary}}
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    result, err := service.Sync(ctx, f.target, "repository", "~/repository-sync", plan, true)
    if err != nil {
        t.Fatal(err)
    }
    f.shell(`test -s "$1/cache/go.mod"; test -s "$1/cache/cmd/loaf/main.go"; test -s "$1/cache/internal/sync/select.go"; test ! -e "$1/cache/docs"; test ! -e "$1/cache/.git"; test ! -e "$1/cache/.cache"; test ! -e "$1/cache/dist"; test ! -e "$1/cache/internal/bundle/assets"`, result.RemoteDir)
    t.Logf("actual repository: %d selected files, %d source bytes", len(plan.Files), plan.Bytes)
}

func TestPackagedSyncPoliciesAndConnectionForms(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    project := t.TempDir()
    root := "/home/researcher/packaged sync 中文"
    sourceFile(t, project, "loaf.toml", "remote_dir = '"+root+"'\n[sync]\nmode = 'mirror'\nexclude = ['project.py']\n")
    for _, name := range []string{"train.py", "old.py", "archive.py", "ignored.py", "global.py", "project.py", ".env", "outputs/result", "checkpoints/model.pt"} {
        sourceFile(t, project, name, "working contents")
    }
    sourceFile(t, project, ".gitignore", "ignored.py\n")
    command(t, "git", "-C", project, "init", "-q")
    // An entirely local preview does not need an alias or deploy a runner.
    code, out := cli(t, project, "sync", "--dry-run", "--json")
    if code != 0 {
        t.Fatal(string(out))
    }
    f.shell(`test ! -e "$HOME/.local/share/loaf"; test ! -e "$1"`, root)
    spaced := filepath.Join(f.dir, `SSH 中文 config`, `config 'quoted'`)
    data, err := os.ReadFile(f.config)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(spaced, data, 0600); err != nil {
        t.Fatal(err)
    }
    for _, args := range [][]string{
        {"machine", "add", "lab", "--ssh", "ssh -F " + ssh.Quote(spaced) + " fixture", "--json"},
        {"machine", "use", "lab", "--json"},
        {"machine", "add", "uri", "--ssh", "ssh -F " + ssh.Quote(f.config) + " ssh://researcher@127.0.0.1:" + f.port, "--json"},
        {"machine", "add", "jump", "--ssh", "ssh -F " + ssh.Quote(f.config) + " jumped", "--json"},
    } {
        code, out := cli(t, project, args...)
        if code != 0 {
            t.Fatal(args, string(out))
        }
    }
    configFile := filepath.Join(project, "config", "loaf", "config.toml")
    file, err := os.OpenFile(configFile, os.O_APPEND|os.O_WRONLY, 0600)
    if err != nil {
        t.Fatal(err)
    }
    _, err = file.WriteString("\n[sync]\nmode = 'update'\nrespect_gitignore = false\nexclude = ['global.py']\n")
    closeErr := file.Close()
    if err != nil || closeErr != nil {
        t.Fatal(err, closeErr)
    }
    syncCLI := func(args ...string) []byte {
        t.Helper()
        code, out := cli(t, project, append([]string{"sync", "--json"}, args...)...)
        if code != 0 {
            t.Fatal(code, string(out))
        }
        return out
    }
    out = syncCLI()
    var result struct {
        Data struct {
            Policy       config.SyncPolicy
            ChangedFiles int `json:"changed_files"`
        }
    }
    if json.Unmarshal(out, &result) != nil || result.Data.Policy.Mode != "mirror" || result.Data.Policy.RespectGitignore {
        t.Fatal(string(out))
    }
    f.shell(`test -f "$1/cache/ignored.py"; test ! -e "$1/cache/.env"; test ! -e "$1/cache/global.py"; test ! -e "$1/cache/project.py"; test ! -e "$1/cache/config"; test ! -e "$1/cache/state"; test ! -e "$1/cache/.git"; test ! -e "$1/cache/outputs"; test ! -e "$1/cache/checkpoints"`, root)
    if err := os.Remove(filepath.Join(project, "old.py")); err != nil {
        t.Fatal(err)
    }
    syncCLI("--mode", "update", "--exclude", "archive.py")
    f.shell(`test -f "$1/cache/old.py"; test -f "$1/cache/archive.py"`, root)
    sourceFile(t, project, "loaf.toml", "remote_dir = '"+root+"'\n[sync]\nmode = 'mirror'\nexclude = ['project.py', 'archive.py']\n")
    syncCLI()
    f.shell(`test ! -e "$1/cache/old.py"; test ! -e "$1/cache/archive.py"`, root)
    for _, alias := range []string{"uri", "jump"} {
        out = syncCLI("--target", alias)
        if json.Unmarshal(out, &result) != nil || result.Data.ChangedFiles != 0 {
            t.Fatal(alias, string(out))
        }
    }
    foreign := "/home/researcher/foreign directory"
    f.shell(`mkdir -p "$1"; printf original >"$1/user-data"`, foreign)
    sourceFile(t, project, "loaf.toml", "remote_dir = '"+foreign+"'\n")
    code, out = cli(t, project, "sync", "--json")
    if code != 1 || !strings.Contains(string(out), `"code":"PROJECT_UNOWNED"`) {
        t.Fatal(code, string(out))
    }
    f.shell(`test "$(cat "$1/user-data")" = original; test ! -e "$1/.loaf-control-v2"`, foreign)
}

func TestInterruptedSyncKeepsCacheUnavailableAndReleasesLock(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    project := t.TempDir()
    sourceFile(t, project, "large.bin", strings.Repeat("data", 256*1024))
    sourceFile(t, project, "train.py", "print(1)\n")
    binary, err := exec.LookPath("rsync")
    if err != nil {
        t.Fatal(err)
    }
    slow := filepath.Join(t.TempDir(), "slow rsync")
    if err := os.WriteFile(slow, []byte("#!/bin/sh\nexec "+ssh.Quote(binary)+" --bwlimit=64 \"$@\"\n"), 0700); err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    bootstrap := execution.Bootstrap{Remote: f.client}
    report, err := f.client.Check(ctx, f.target, "~/interrupted", true)
    if err != nil {
        t.Fatal(err)
    }
    runner, err := bootstrap.Verify(ctx, f.target, report.Arch, true)
    if err != nil {
        t.Fatal(err)
    }
    root := "/home/researcher/interrupted"
    regular := ssh.Rsync{Client: f.client, Binary: binary}
    if _, err := regular.Sync(ctx, f.target, runner.Path, project, ssh.SyncInput{Root: root, ProjectID: "interrupted", Mode: "mirror", Paths: []string{"train.py"}}, true); err != nil {
        t.Fatal(err)
    }
    existing := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "start", Root: root, ProjectID: "interrupted", RunID: "existing", Args: []string{"/bin/sh", "-c", "echo ready; while [ ! -f release ]; do sleep .05; done; exit 7"}}
    if record, err := f.client.Control(ctx, f.target, runner.Path, existing, true); err != nil || record.Status != "running" {
        t.Fatal(record, err)
    }
    sourceFile(t, project, "train.py", "print(2)\n")
    input := ssh.SyncInput{Root: root, ProjectID: "interrupted", Mode: "mirror", Paths: []string{"large.bin", "train.py"}}
    transferCtx, abort := context.WithCancel(ctx)
    defer abort()
    done := make(chan error, 1)
    go func() {
        _, err := (ssh.Rsync{Client: f.client, Binary: slow}).Sync(transferCtx, f.target, runner.Path, project, input, true)
        done <- err
    }()
    wait(t, 10*time.Second, func() bool {
        return f.remoteSQL(root, "SELECT complete FROM sync_state WHERE singleton=1") == "0"
    })
    _, err = (ssh.Rsync{Client: f.client, Binary: binary}).Sync(ctx, f.target, runner.Path, project, input, true)
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "PROJECT_BUSY" {
        t.Fatal("concurrent sync entered", err)
    }
    request := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "start", Root: root, ProjectID: "interrupted", RunID: "blocked", Args: []string{"/bin/true"}}
    _, err = f.client.Control(ctx, f.target, runner.Path, request, true)
    if err == nil || err.Error() != "PROJECT_BUSY" {
        t.Fatal("start entered during sync", err)
    }
    abort()
    select {
    case err := <-done:
        if !errors.As(err, &failure) || failure.Code != "SYNC_CANCELED" {
            t.Fatal(err)
        }
    case <-time.After(5 * time.Second):
        t.Fatal("cancel did not close sender and SSH")
    }
    wait(t, 5*time.Second, func() bool {
        _, err = f.client.Control(ctx, f.target, runner.Path, request, true)
        return err != nil && err.Error() == "SYNC_INCOMPLETE"
    })
    f.shell(`test ! -e "$1/runs/blocked"; ! pgrep -u researcher -x rsync`, root)
    existing.Action = "status"
    if record, err := f.client.Control(ctx, f.target, runner.Path, existing, true); err != nil || record.Status != "running" {
        t.Fatal("cancel affected an accepted run", record, err)
    }
    if _, err := (ssh.Rsync{Client: f.client, Binary: binary}).Sync(ctx, f.target, runner.Path, project, input, true); err != nil {
        t.Fatal(err)
    }
    if f.remoteSQL(root, "SELECT complete FROM sync_state WHERE singleton=1") != "1" {
        t.Fatal("sync did not commit completion")
    }
    f.shell(`test -s "$1/cache/large.bin"`, root)
    f.shell(`grep -q 'print(1)' "$1/runs/existing/work/train.py"; grep -q 'print(2)' "$1/cache/train.py"; touch "$1/runs/existing/work/release"`, root)
    wait(t, 5*time.Second, func() bool {
        record, err := f.client.Control(ctx, f.target, runner.Path, existing, true)
        return err == nil && record.Status == "failed" && record.ExitCode != nil && *record.ExitCode == 7
    })
}
