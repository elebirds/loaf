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

// This calls the application API without a parser, renderer, terminal or transport.
func TestRunPreviewResolvesAbsentAndZeroWithoutWritingState(t *testing.T) {
    root := t.TempDir()
    if err := os.WriteFile(filepath.Join(root, "loaf.toml"), []byte("target='project'\n[run]\ncommand='printf done'\nwait='3s'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    store := config.New(t.TempDir(), "", "")
    state := local.New(store.StateDir)
    settings := config.Global{Targets: map[string]config.Target{"project": {Destination: "machine", RemoteBase: "~/projects"}, "flag": {Destination: "other", RemoteBase: "~/projects"}}, Run: config.RunPreferences{Wait: "9s"}}
    app := New(Dependencies{Config: store, State: state, CWD: root}, settings)
    for _, test := range []struct {
        name             string
        wait             *string
        target           *string
        millis           int64
        selected, source string
    }{{name: "absent uses project", millis: 3000, selected: "project", source: "project"}, {name: "explicit zero wins", wait: new("0s"), target: new("flag"), millis: 0, selected: "flag", source: "flag"}} {
        t.Run(test.name, func(t *testing.T) {
            result, err := app.Projects.Run(context.Background(), RunInput{Wait: test.wait, Target: test.target, DryRun: true}, RunFeedback{})
            if err != nil {
                t.Fatal(err)
            }
            if result.Preview == nil || result.Run != nil || result.Preview.WaitMillis != test.millis || result.Preview.Target != test.selected || result.Preview.Provenance["wait"][0] != test.source {
                t.Fatalf("%+v", result.Preview)
            }
        })
    }
    if _, err := os.Stat(state.Path); !errors.Is(err, os.ErrNotExist) {
        t.Fatalf("preview created local state: %v", err)
    }
}

func TestPrepareCleanRequiresExplicitIDsBeforeDependencies(t *testing.T) {
    _, err := (&Projects{}).PrepareClean(context.Background(), CleanInput{})
    var failure *fault.Error
    if !errors.As(err, &failure) || failure.Code != "BAD_CONFIG" || failure.Message != "argument_count" {
        t.Fatal(err)
    }
}
