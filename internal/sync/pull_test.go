package sync

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

type outputRemote struct {
    selection wire.OutputSelection
    req       wire.Request
    target    config.Target
    err       error
}

func (r *outputRemote) RunnerArch(_ context.Context, target config.Target, batch bool) (string, error) {
    r.target = target
    return "aarch64", nil
}

func (r *outputRemote) Outputs(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (wire.OutputSelection, error) {
    r.req = req
    return r.selection, r.err
}

type outputDownloader struct {
    calls   int
    input   ssh.PullInput
    err     error
    retried bool
}

func (d *outputDownloader) Pull(_ context.Context, _ config.Target, _ string, _ string, in ssh.PullInput, _ bool) (ssh.Transfer, error) {
    d.calls++
    d.input = in
    return ssh.Transfer{ChangedFiles: 2, Retried: d.retried}, d.err
}

func TestPullUsesSavedLocationAndKeepsPartialResult(t *testing.T) {
    remote := &outputRemote{selection: wire.OutputSelection{Available: []string{"outputs"}, Missing: []string{"checkpoints"}}}
    downloader := &outputDownloader{}
    svc := PullService{Remote: remote, Receive: downloader, Runner: func(_ context.Context, target config.Target, _ string, _ bool) (string, error) {
        if target.Destination != "saved-host" {
            t.Fatal(target)
        }
        return "/current-helper", nil
    }}
    ref := config.RunRef{ID: "run", ProjectID: "project", RemoteDir: "/saved-location", Connection: config.Target{Destination: "saved-host"}, Runner: "/saved-runner", Version: wire.Version}
    destination := canonicalTemp(t)
    result, err := svc.Pull(context.Background(), ref, destination, []string{"outputs/", "checkpoints"}, config.PullPolicy{Checksum: true}, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "OUTPUT_MISSING" || result.ChangedFiles == nil || *result.ChangedFiles != 2 || !reflect.DeepEqual(result.MissingPaths, []string{"checkpoints"}) || !reflect.DeepEqual(result.DownloadedPaths, []string{"outputs"}) {
        t.Fatal(result, err)
    }
    if remote.req.Root != ref.RemoteDir || remote.req.RunID != ref.ID || remote.req.Version != wire.Version || downloader.calls != 1 || !downloader.input.Checksum || !reflect.DeepEqual(downloader.input.Paths, []string{"outputs"}) {
        t.Fatal(remote.req, downloader)
    }
    remote.selection = wire.OutputSelection{Missing: []string{"outputs", "checkpoints"}}
    result, err = svc.Pull(context.Background(), ref, filepath.Join(destination, "missing"), []string{"outputs", "checkpoints"}, config.PullPolicy{}, true)
    if !errors.As(err, &f) || f.Code != "OUTPUT_MISSING" || downloader.calls != 1 || result.ChangedFiles != nil {
        t.Fatal(result, err)
    }
    if _, err := os.Stat(filepath.Join(destination, "missing")); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("all-missing outputs created a destination", err)
    }
    remote.selection = wire.OutputSelection{Available: []string{"outputs"}}
    downloader.err = fault.New("PULL_FAILED", "pull_failed", nil)
    result, err = svc.Pull(context.Background(), ref, destination, []string{"outputs"}, config.PullPolicy{}, true)
    if err == nil || result.ChangedFiles != nil || len(result.DownloadedPaths) != 0 {
        t.Fatal("claimed a failed transfer completed", result, err)
    }
    remote.selection = wire.OutputSelection{Available: []string{"../outside"}}
    before := downloader.calls
    _, err = svc.Pull(context.Background(), ref, destination, []string{"outputs"}, config.PullPolicy{}, true)
    if !errors.As(err, &f) || f.Code != "BAD_RESPONSE" || downloader.calls != before {
        t.Fatal("trusted an invalid remote selection", err)
    }
}

func TestRecoveredPullDoesNotClaimALastAttemptChangeCount(t *testing.T) {
    remote := &outputRemote{selection: wire.OutputSelection{Available: []string{"outputs"}}}
    downloader := &outputDownloader{retried: true}
    svc := PullService{Remote: remote, Receive: downloader, Runner: func(context.Context, config.Target, string, bool) (string, error) { return "/helper", nil }}
    ref := config.RunRef{ID: "run", ProjectID: "project", RemoteDir: "/saved-location", Connection: config.Target{Destination: "saved-host"}, Version: wire.Version}
    result, err := svc.Pull(context.Background(), ref, canonicalTemp(t), []string{"outputs"}, config.PullPolicy{}, true)
    if err != nil || result.ChangedFiles != nil || !reflect.DeepEqual(result.DownloadedPaths, []string{"outputs"}) {
        t.Fatal("reported only the last attempt as a complete change count", result, err)
    }
}

func TestPullRejectsAnotherVersionBeforeConnecting(t *testing.T) {
    result, err := (PullService{}).Pull(context.Background(), config.RunRef{ID: "run", Version: "another-version"}, t.TempDir(), []string{"outputs"}, config.PullPolicy{}, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "VERSION_MISMATCH" || result.RunID != "run" || result.ChangedFiles != nil {
        t.Fatal(result, err)
    }
}

func canonicalTemp(t *testing.T) string {
    t.Helper()
    dir, err := filepath.EvalSymlinks(t.TempDir())
    if err != nil {
        t.Fatal(err)
    }
    return dir
}

func TestOutputPathsAreLiteralAndValidated(t *testing.T) {
    for _, name := range []string{".", "", "../outside", "/absolute", "dir/../escape", "bad\x00name", "bad\nname", "dir//file"} {
        if _, err := OutputPaths([]string{name}); err == nil {
            t.Fatal("accepted unsafe output", name)
        }
    }
    result, err := OutputPaths([]string{"中文 space/", "literal[1]*?", "中文 space"})
    if err != nil || !reflect.DeepEqual(result, []string{"中文 space", "literal[1]*?"}) {
        t.Fatal(result, err)
    }
}

func TestLocalDownloadBoundariesAndDirectoryLock(t *testing.T) {
    project := canonicalTemp(t)
    policy := config.PullPolicy{BaseDir: "collected"}
    destination, err := Destination(project, filepath.Join(project, "src"), "", "run", policy)
    if err != nil || destination != filepath.Join(project, "collected", "run") {
        t.Fatal(destination, err)
    }
    exact, err := Destination(project, filepath.Join(project, "src"), "../export", "run", policy)
    if err != nil || exact != filepath.Join(project, "export") {
        t.Fatal(exact, err)
    }
    for _, dir := range []string{"/", project, filepath.Dir(project)} {
        if _, err := Destination(project, project, dir, "run", policy); err == nil {
            t.Fatal("accepted dangerous destination", dir)
        }
    }
    lock, err := openDestination(destination, []string{"outputs"})
    if err != nil {
        t.Fatal(err)
    }
    _, err = openDestination(destination, []string{"outputs"})
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "PULL_BUSY" {
        t.Fatal("directory not locked", err)
    }
    lock.Close()
    outside := canonicalTemp(t)
    if err := os.Symlink(outside, filepath.Join(destination, "outputs")); err != nil {
        t.Fatal(err)
    }
    for _, paths := range [][]string{{"outputs"}, {"outputs/nested/file"}} {
        if lock, err := openDestination(destination, paths); err == nil {
            lock.Close()
            t.Fatal("accepted escaping destination link", paths)
        }
    }
    if _, err := Destination(project, project, filepath.Join(destination, "outputs"), "run", policy); err == nil {
        t.Fatal("accepted symlink destination root")
    }
}

func TestConfiguredResultDirectoryIsNeverUploaded(t *testing.T) {
    root := canonicalTemp(t)
    for _, name := range []string{"train.py", "exports/run/metrics.json"} {
        file := filepath.Join(root, name)
        if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
            t.Fatal(err)
        }
    }
    no := false
    plan, err := Select(context.Background(), root, SelectOptions{RespectGitignore: &no, ResultsDir: filepath.Join(root, "exports")})
    if err != nil || len(plan.Files) != 1 || plan.Files[0].Path != "train.py" || len(plan.Excluded) != 1 || plan.Excluded[0].Reason != "results" {
        t.Fatal(plan, err)
    }
}
