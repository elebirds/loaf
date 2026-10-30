package client

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/state/local"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type uxFPuller struct {
    refs         []task.RunRef
    destinations []string
    policies     []config.PullPolicy
}

func (p *uxFPuller) Pull(_ context.Context, ref task.RunRef, destination string, paths []string, policy config.PullPolicy, _ bool) (sources.PullResult, error) {
    p.refs = append(p.refs, ref)
    p.destinations = append(p.destinations, destination)
    p.policies = append(p.policies, policy)
    return sources.PullResult{RunID: ref.ID, Target: ref.Target, Destination: destination, Paths: paths, Policy: policy}, nil
}

func uxFProject(t *testing.T) (*Projects, []task.RunRef, *uxFPuller) {
    t.Helper()
    root, err := filepath.EvalSymlinks(t.TempDir())
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte("[pull]\nbase_dir='project-results'\nchecksum=false\n"), 0600); err != nil {
        t.Fatal(err)
    }
    cwd := filepath.Join(root, "source")
    if err := os.Mkdir(cwd, 0700); err != nil {
        t.Fatal(err)
    }
    store := config.New(t.TempDir(), "", "")
    state := local.New(store.StateDir)
    project, err := state.Select(context.Background(), root, "")
    if err != nil {
        t.Fatal(err)
    }
    refs := []task.RunRef{}
    for _, id := range []string{"r_000000000000000000000001", "r_000000000000000000000002"} {
        attempted := time.Now().UTC()
        ref := task.RunRef{ID: id, ProjectID: project.ID, Target: "saved", Connection: config.Target{Destination: "original", RemoteBase: "~/projects"}, RemoteDir: "/remote/project", Runner: "/helper", Version: wire.Version, Outputs: []string{"outputs", "checkpoints"}, CreatedAt: attempted, AttemptedAt: &attempted, Submission: "submitted"}
        if err := state.SaveRun(context.Background(), ref); err != nil {
            t.Fatal(err)
        }
        refs = append(refs, ref)
    }
    puller := &uxFPuller{}
    c := &Projects{Config: store, State: state, CWD: cwd, Runs: &execution.Service{Index: state}, Puller: puller, Settings: config.Global{Pull: config.PullSettings{BaseDir: "user-results", Checksum: new(true)}, Targets: map[string]config.Target{"saved": {Destination: "replacement"}}}}
    return c, refs, puller
}

func TestUXFPullPreviewMatchesEffectivePolicyAndExactDestinations(t *testing.T) {
    c, refs, puller := uxFProject(t)
    defaults, err := c.PullPreferences(context.Background())
    if err != nil || defaults.BaseSource != "project" || defaults.ChecksumSource != "project" || defaults.Policy.Checksum {
        t.Fatal(defaults, err)
    }
    preview, err := c.PreviewPull(context.Background(), PullInput{IDs: []string{refs[0].ID}})
    if err != nil || preview.DestinationSource != "project" || preview.Runs[0].Destination != filepath.Join(defaults.BaseDirectory, refs[0].ID) {
        t.Fatal(preview, err)
    }
    if _, err := os.Stat(preview.Runs[0].Destination); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("preview wrote a destination", err)
    }
    if len(puller.refs) != 0 {
        t.Fatal("preview downloaded")
    }
    for _, test := range []struct {
        name   string
        input  PullInput
        source string
    }{
        {"exact relative", PullInput{IDs: []string{refs[0].ID}, To: new("../export"), Paths: []string{"outputs/中文 space,comma[1]*?"}, Checksum: new(false)}, "to"},
        {"batch isolation", PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: new("../comparison")}, "into"},
        {"single batch", PullInput{IDs: []string{refs[0].ID}, Into: new("../comparison")}, "into"},
    } {
        t.Run(test.name, func(t *testing.T) {
            p, err := c.PreviewPull(context.Background(), test.input)
            if err != nil || p.DestinationSource != test.source {
                t.Fatal(p, err)
            }
            if p.Input.Checksum == nil || *p.Input.Checksum {
                t.Fatal(p)
            }
            result, err := c.Pull(context.Background(), p.Input, nil)
            if err != nil {
                t.Fatal(result, err)
            }
            start := len(puller.refs) - len(p.Runs)
            for i, row := range p.Runs {
                if row.Destination != puller.destinations[start+i] || puller.refs[start+i].Connection.Destination != "original" {
                    t.Fatal(row, puller)
                }
                if test.source == "into" && filepath.Base(row.Destination) != row.ID {
                    t.Fatal(row)
                }
            }
            if test.input.Paths != nil && !reflect.DeepEqual(p.Input.Paths, test.input.Paths) {
                t.Fatal(p.Input.Paths)
            }
        })
    }
}

func TestUXFPullDefaultsIndependentLayersAndExplicitFalse(t *testing.T) {
    c, refs, _ := uxFProject(t)
    root := filepath.Dir(c.CWD)
    for _, test := range []struct {
        text, baseSource, checksumSource string
        checksum                         bool
    }{
        {"", "user", "user", true},
        {"[pull]\nchecksum=false\n", "user", "project", false},
        {"[pull]\nbase_dir='another'\n", "project", "user", true},
    } {
        if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte(test.text), 0600); err != nil {
            t.Fatal(err)
        }
        p, err := c.PreviewPull(context.Background(), PullInput{IDs: []string{refs[0].ID}})
        if err != nil || p.Preferences.BaseSource != test.baseSource || p.Preferences.ChecksumSource != test.checksumSource || p.Preferences.Policy.Checksum != test.checksum {
            t.Fatal(p, err)
        }
        p, err = c.PreviewPull(context.Background(), PullInput{IDs: []string{refs[0].ID}, Checksum: new(false)})
        if err != nil || p.Preferences.Policy.Checksum || p.Preferences.ChecksumSource != "flag" {
            t.Fatal(p, err)
        }
    }
    c.Settings.Pull = config.PullSettings{}
    if err := os.WriteFile(filepath.Join(root, "loaf.toml"), nil, 0600); err != nil {
        t.Fatal(err)
    }
    p, err := c.PullPreferences(context.Background())
    if err != nil || p.BaseSource != "default" || p.Policy.BaseDir != "loaf-results" || p.Policy.Checksum {
        t.Fatal(p, err)
    }
}

func TestUXFPullPreviewPreservesExplicitDestinationOverride(t *testing.T) {
    c, refs, _ := uxFProject(t)
    if err := os.WriteFile(filepath.Join(filepath.Dir(c.CWD), "loaf.toml"), []byte("[pull]\nbase_dir='.'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    in := PullInput{IDs: []string{refs[0].ID}, To: new("exports")}
    p, err := c.PreviewPull(context.Background(), in)
    if err != nil || p.Runs[0].Destination != filepath.Join(c.CWD, "exports") {
        t.Fatal(p, err)
    }
    if _, err := c.Pull(context.Background(), in, nil); err != nil {
        t.Fatal(err)
    }
    in.To = nil
    if _, err := c.PreviewPull(context.Background(), in); err == nil {
        t.Fatal("unsafe default accepted")
    }
}

func TestUXFPullPreviewRejectsInvalidInputsWithoutTransfer(t *testing.T) {
    c, refs, puller := uxFProject(t)
    for _, in := range []PullInput{
        {IDs: []string{refs[0].ID, refs[1].ID}},
        {IDs: []string{refs[0].ID}, To: new("")},
        {IDs: []string{refs[0].ID}, Paths: []string{"../escape"}},
        {IDs: []string{refs[0].ID}, Paths: []string{}},
        {IDs: []string{refs[0].ID}, To: new(filepath.Dir(c.CWD))},
        {IDs: []string{refs[0].ID}, Into: new("exports"), Concurrency: new(2)},
    } {
        if _, err := c.PreviewPull(context.Background(), in); err == nil {
            t.Fatal("accepted", in)
        }
    }
    if len(puller.refs) != 0 {
        t.Fatal("validation transferred")
    }
}

func TestUXFBatchProgressNamesEachAttemptAndCancellationCoverage(t *testing.T) {
    c, refs, puller := uxFProject(t)
    in := PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: new("comparison")}
    events := []Progress{}
    result, err := c.Pull(context.Background(), in, func(event Progress) { events = append(events, event) })
    if err != nil || result.Batch.Completed != 2 || len(events) != 2 {
        t.Fatal(result, events, err)
    }
    for i, event := range events {
        if event.RunID != refs[i].ID || event.Stage != "pull" {
            t.Fatal(events)
        }
    }
    before := len(puller.refs)
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    result, err = c.Pull(ctx, in, nil)
    if uxFClientCode(err) != "PULL_PARTIAL" || result.Batch.Unexamined != 2 || result.Batch.Completed != 0 || len(puller.refs) != before {
        t.Fatal(result, err)
    }
}
func uxFClientCode(err error) string {
    var f *fault.Error
    if errors.As(err, &f) {
        return f.Code
    }
    return ""
}

func TestUXFBatchValidationRetainsExistingErrorData(t *testing.T) {
    c, refs, puller := uxFProject(t)
    result, err := c.Pull(context.Background(), PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: new("comparison"), Paths: []string{"../escape"}}, nil)
    if err == nil || result.Batch == nil || result.Batch.Selected != 2 || len(result.Batch.Runs) != 2 || len(puller.refs) != 0 {
        t.Fatal("batch selection failure lost its existing data", result, err)
    }
    root := filepath.VolumeName(c.CWD) + string(filepath.Separator)
    result, err = c.Pull(context.Background(), PullInput{IDs: []string{refs[0].ID, refs[1].ID}, Into: &root}, nil)
    if err == nil || result.Batch == nil || result.Batch.Selected != 0 || len(result.Batch.Runs) != 0 {
        t.Fatal("early destination failure changed its batch data", result, err)
    }
}
