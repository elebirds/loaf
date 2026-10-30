package client

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/local"
)

func TestUXCRunRequirementsAreOfflineAndDoNotHideInvalidInputs(t *testing.T) {
    for _, test := range []struct {
        name, file, selected string
        in                   RunInput
        command, target      bool
        failure              string
    }{
        {name: "both missing", command: true, target: true},
        {name: "project command", file: "[run]\ncommand='echo ready'", target: true},
        {name: "project target", file: "target='lab'", command: true, selected: "lab"},
        {name: "complete", file: "target='lab'\n[run]\ncommand='echo ready'", selected: "lab"},
        {name: "explicit argv", in: RunInput{Args: []string{"echo", "$HOME"}}, target: true},
        {name: "stale implicit target", file: "target='removed'", command: true, target: true},
        {name: "wrong explicit target", in: RunInput{Target: new("removed")}, command: true, failure: "target_unknown"},
        {name: "invalid argv", in: RunInput{Args: []string{""}}, failure: "run_command"},
        {name: "invalid config", file: "[run]\ncommand=123", failure: "project_read"},
    } {
        t.Run(test.name, func(t *testing.T) {
            root, home := t.TempDir(), t.TempDir()
            store := config.New(home, "", "")
            state := local.New(store.StateDir)
            if test.file != "" {
                if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte(test.file), 0600); err != nil {
                    t.Fatal(err)
                }
            }
            app := New(Dependencies{Config: store, State: state, CWD: root}, config.Global{Targets: map[string]config.Target{"lab": {Destination: "fixture", RemoteBase: "~/projects"}}})
            got, err := app.Projects.RunRequirements(context.Background(), test.in)
            if test.failure != "" {
                var f *fault.Error
                if !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
                    t.Fatal(got, err)
                }
            } else if err != nil || got.CommandMissing != test.command || got.TargetMissing != test.target || got.Target != test.selected {
                t.Fatal(got, err)
            }
            if _, err := os.Stat(state.Path); !os.IsNotExist(err) {
                t.Fatal("requirements created state", err)
            }
        })
    }
}

func TestUXCSyncTargetDoesNotRequireCommandOrWriteState(t *testing.T) {
    root := t.TempDir()
    store := config.New(t.TempDir(), "", "")
    state := local.New(store.StateDir)
    app := New(Dependencies{Config: store, State: state, CWD: root}, config.Global{Targets: map[string]config.Target{"lab": {Destination: "fixture", RemoteBase: "~/projects"}}})
    got, err := app.Projects.SyncTarget(context.Background(), new("lab"))
    if err != nil || got != "lab" {
        t.Fatal(got, err)
    }
    if _, err := os.Stat(state.Path); !os.IsNotExist(err) {
        t.Fatal("target resolution created state", err)
    }
}
