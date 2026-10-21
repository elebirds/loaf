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
)

func TestTargetsAreLocalReadOnlyAndSorted(t *testing.T) {
    o := fixture(t)
    code, out, stderr := run(t, o, "targets", "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, `"targets":[]`) {
        t.Fatal(code, out, stderr)
    }
    for _, file := range []string{o.Store.ConfigFile, o.Store.IndexFile} {
        if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
            t.Fatal("listing created user state", file, err)
        }
    }
    for _, name := range []string{"zeta", "alpha"} {
        if code, out, stderr := run(t, o, "add", name, "--ssh", "ssh -p 2222 user@invalid.example", "--json"); code != 0 {
            t.Fatal(code, out, stderr)
        }
    }
    code, out, stderr = run(t, o, "targets", "--json")
    var response struct {
        Data targetsResult `json:"data"`
    }
    if err := json.Unmarshal([]byte(out), &response); err != nil || code != 0 || stderr != "" || len(response.Data.Targets) != 2 || response.Data.Targets[0].Name != "alpha" || response.Data.Project.Target != "" {
        t.Fatal(code, out, stderr, err)
    }
    if code, out, stderr := run(t, o, "targets", "zeta", "--json"); code != 0 || !strings.Contains(out, `"name":"zeta"`) || strings.Contains(out, `"name":"alpha"`) {
        t.Fatal(code, out, stderr)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, command := range []string{"targets", "remove"} {
            if code, out, stderr := run(t, o, "--lang", lang, command, "--help"); code != 0 || stderr != "" || out == "" || strings.Contains(out, "<no value>") {
                t.Fatal(code, out, stderr)
            }
        }
        if code, out, stderr := run(t, o, "--lang", lang, "targets"); code != 0 || stderr != "" || !strings.Contains(out, "alpha") || !strings.Contains(out, "2222") {
            t.Fatal(code, out, stderr)
        }
    }
    for _, args := range [][]string{{"targets", "missing"}, {"targets", "--target", "zeta"}, {"remove", "--target", "zeta", "alpha"}} {
        if code, out, stderr := run(t, o, append(args, "--json")...); code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatal(args, code, out, stderr)
        }
    }
}

func TestRemovingSelectedMachinePreservesRunBindingsAndTOML(t *testing.T) {
    o, _ := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    root, _, err := config.Discover(context.Background(), o.CWD)
    if err != nil {
        t.Fatal(err)
    }
    project, err := o.Store.Existing(root)
    if err != nil {
        t.Fatal(err)
    }
    before, err := o.Store.Runs(project.ID)
    if err != nil || len(before) != 1 {
        t.Fatal(before, err)
    }
    original := "target = 'fallback'\n"
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(original), 0600); err != nil {
        t.Fatal(err)
    }
    if code, out, stderr := run(t, o, "add", "fallback", "--ssh", "fallback", "--json"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "remove", "lab", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"selected":true`) {
        t.Fatal(code, out, stderr)
    }
    after, err := o.Store.Runs(project.ID)
    if err != nil || !reflect.DeepEqual(before, after) {
        t.Fatal(after, err)
    }
    if data, err := os.ReadFile(filepath.Join(o.CWD, "loaf.toml")); err != nil || string(data) != original {
        t.Fatal(string(data), err)
    }
    if code, out, stderr := run(t, o, "targets", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"target":"lab"`) || !strings.Contains(out, `"available":false`) || !strings.Contains(out, `"source":"personal"`) {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "check", "--json"); code != 2 || stderr != "" || !strings.Contains(out, "lab") {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "ps", before[0].ID, "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"target":"lab"`) {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "logs", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"target":"lab"`) {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "use", "fallback", "--json"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    cfg, err := o.Store.Load()
    if err != nil {
        t.Fatal(err)
    }
    selected, err := o.Store.Existing(root)
    if err != nil {
        t.Fatal(err)
    }
    if name, _, err := config.Resolve("", selected, config.ProjectFile{}, cfg); err != nil || name != "fallback" {
        t.Fatal(name, err)
    }
}

func TestRemovingSharedChoiceDoesNotCreatePersonalFallback(t *testing.T) {
    o := fixture(t)
    original := "target = 'lab'\n"
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(original), 0600); err != nil {
        t.Fatal(err)
    }
    for _, name := range []string{"lab", "other"} {
        if code, out, stderr := run(t, o, "add", name, "--ssh", name, "--json"); code != 0 {
            t.Fatal(code, out, stderr)
        }
    }
    if code, out, stderr := run(t, o, "targets", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"source":"project"`) || !strings.Contains(out, `"selected":true`) {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "remove", "lab", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"selected":true`) {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "check", "--json"); code != 2 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
    if code, out, stderr := run(t, o, "targets", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"target":"lab"`) || !strings.Contains(out, `"available":false`) {
        t.Fatal(code, out, stderr)
    }
    if _, err := os.Stat(o.Store.IndexFile); !os.IsNotExist(err) {
        t.Fatal("local management created personal project selection", err)
    }
    if data, err := os.ReadFile(filepath.Join(o.CWD, "loaf.toml")); err != nil || string(data) != original {
        t.Fatal(string(data), err)
    }
}
