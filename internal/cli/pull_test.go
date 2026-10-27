package cli

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

type fakePuller struct {
    calls       int
    ref         task.RunRef
    destination string
    paths       []string
    policy      config.PullPolicy
    batch       bool
    missing     bool
}

func (p *fakePuller) Pull(_ context.Context, ref task.RunRef, destination string, paths []string, policy config.PullPolicy, batch bool) (sources.PullResult, error) {
    p.calls++
    p.ref, p.destination, p.paths, p.policy, p.batch = ref, destination, paths, policy, batch
    count := 1
    result := sources.PullResult{RunID: ref.ID, Target: ref.Target, Destination: destination, Paths: paths, DownloadedPaths: paths, MissingPaths: []string{}, ChangedFiles: &count, Policy: policy}
    if p.missing {
        result.MissingPaths = []string{"checkpoints"}
        return result, fault.New("OUTPUT_MISSING", "pull_missing", nil)
    }
    return result, nil
}

func TestPublicPullSelectionPreferencesAndBothLanguages(t *testing.T) {
    o, _ := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    puller := &fakePuller{}
    o.Puller = puller
    projectRoot, err := filepath.EvalSymlinks(o.CWD)
    if err != nil {
        t.Fatal(err)
    }
    base, err := os.ReadFile(o.Config.ConfigFile)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(o.Config.ConfigFile, append(base, []byte("\n[pull]\nbase_dir='retrieved'\nchecksum=true\n")...), 0600); err != nil {
        t.Fatal(err)
    }
    // New project output settings do not replace the submitted selection.
    text := "[outputs]\npaths=['not-this-run']\n[pull]\nchecksum=false\n"
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(text), 0600); err != nil {
        t.Fatal(err)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "pull", "--json")
        if code != 0 || stderr != "" || !json.Valid([]byte(out)) || strings.Count(strings.TrimSpace(out), "\n") != 0 || !puller.batch || !reflect.DeepEqual(puller.paths, []string{"outputs"}) || puller.policy.Checksum || puller.policy.BaseDir != "retrieved" || puller.destination != filepath.Join(projectRoot, "retrieved", puller.ref.ID) {
            t.Fatal(code, out, stderr, puller)
        }
        code, out, stderr = run(t, o, "--lang", lang, "pull", "--help")
        if code != 0 || stderr != "" || !strings.Contains(out, "--checksum") || !strings.Contains(out, "--path") || strings.Contains(out, "--target") {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "--lang", lang, "pull", puller.ref.ID, "--path", "custom,comma", "--path", "中文 space", "--to", "export", "--checksum", "--json")
        if code != 0 || stderr != "" || !puller.policy.Checksum || !reflect.DeepEqual(puller.paths, []string{"custom,comma", "中文 space"}) || filepath.Base(puller.destination) != "export" {
            t.Fatal(code, out, stderr, puller)
        }
        puller.missing = true
        code, out, stderr = run(t, o, "--lang", lang, "pull", "--json")
        if code != 1 || stderr != "" || !strings.Contains(out, `"code":"OUTPUT_MISSING"`) || !strings.Contains(out, `"missing_paths":["checkpoints"]`) {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "--lang", lang, "pull")
        if code != 1 || !strings.Contains(out, "checkpoints") || stderr == "" || strings.Contains(out, "Outputs downloaded") || strings.Contains(out, "已下载任务") {
            t.Fatal(code, out, stderr)
        }
        puller.missing = false
    }
    before := puller.calls
    for _, args := range [][]string{{"pull", "one", "two"}, {"pull", "--target", "lab"}, {"pull", "--checksum=bad"}, {"pull", "--to="}, {"pull", "--path", ".."}, {"pull", "--path", "."}, {"pull", "--path="}} {
        code, out, stderr := run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" || !strings.Contains(out, `"code":"BAD_CONFIG"`) || puller.calls != before {
            t.Fatal(args, code, out, stderr)
        }
    }
}

func TestPullStringArgumentsDoNotBecomeGlobalOptions(t *testing.T) {
    for _, args := range [][]string{{"pull", "--path", "--json"}, {"pull", "--to", "--lang=fr"}} {
        parser, err := newParser(&grammar{})
        if err != nil {
            t.Fatal(err)
        }
        early := bootstrap(parser.Model, args)
        if early.json || early.languageSet {
            t.Fatal(args, early)
        }
    }
}

func TestConfiguredResultBaseIsExcludedBySync(t *testing.T) {
    o, _ := runFixture(t)
    text := "[pull]\nbase_dir='exports'\n"
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(text), 0600); err != nil {
        t.Fatal(err)
    }
    if err := os.MkdirAll(filepath.Join(o.CWD, "exports", "old-run"), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(o.CWD, "exports", "old-run", "data.txt"), []byte("result"), 0600); err != nil {
        t.Fatal(err)
    }
    syncer := &fakeSyncer{}
    o.Syncer = syncer
    code, out, stderr := run(t, o, "sync", "--json")
    if code != 0 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
    for _, file := range syncer.Plan.Files {
        if strings.HasPrefix(file.Path, "exports/") {
            t.Fatal("downloaded output selected for upload", file)
        }
    }
}
