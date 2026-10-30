package cli

import (
    "context"
    "errors"
    "os"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

func TestUXCSyncMenuPreviewAndBackNeedNoMachine(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        o := fixture(t)
        syncer := &fakeSyncer{}
        o.Syncer = syncer
        c := uxCCall(t, o, language, "4\n", "sync")
        data, err := c.uxCSync(context.Background())
        if err != nil || data != nil || syncer.Calls != 0 {
            t.Fatal(data, err)
        }
        if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
            t.Fatal("preview created state", err)
        }
        c.policy.Batch = true
        if _, err := c.uxCSync(context.Background()); !errors.Is(err, interaction.ErrNonInteractive) {
            t.Fatal(err)
        }
    }
}

func TestUXCSyncMissingTargetReviewsChosenMachineBeforeTransfer(t *testing.T) {
    for _, input := range []string{"1\n1\n4\n", "1\n1\n1\n"} {
        o, _, _, _ := uxCFixture(t, "")
        syncer := &fakeSyncer{}
        o.Syncer = syncer
        c := uxCCall(t, o, "en", input, "sync", "--mode", "update", "--respect-gitignore=false", "--exclude", "scratch/")
        data, err := c.uxCSyncCommand(context.Background())
        if err != nil {
            t.Fatal(err, uxCString(c.output.err))
        }
        if strings.HasSuffix(input, "4\n") {
            if data != nil || syncer.Calls != 0 {
                t.Fatal("selection triggered sync", data)
            }
            if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
                t.Fatal("selection created state")
            }
        } else {
            result := data.(client.SyncResult)
            if syncer.Calls != 1 || syncer.Batch || result.Target != "lab" || result.Policy.Mode != "update" || result.Policy.RespectGitignore || !reflect.DeepEqual(result.Policy.Exclude, []string{"scratch/"}) {
                t.Fatal(result, syncer)
            }
            project, _ := o.State.Existing(o.CWD)
            if project.Target != "" {
                t.Fatal("sync persisted choice", project)
            }
        }
        if !strings.Contains(uxCString(c.output.err), "lab") {
            t.Fatal("destination was not reviewed")
        }
    }
}

func TestUXCSyncDirectDryRunAndFailureKeepExistingContract(t *testing.T) {
    for _, jsonMode := range []bool{false, true} {
        o, _, _, _ := uxCFixture(t, "target='lab'")
        syncer := &fakeSyncer{}
        o.Syncer = syncer
        args := []string{"sync"}
        if jsonMode {
            args = append(args, "--json")
        }
        c := uxCCall(t, o, "en", "", args...)
        if _, err := c.uxCSyncCommand(context.Background()); err != nil || syncer.Calls != 1 {
            t.Fatal(err, syncer)
        }
        syncer.Err = fault.New("PROJECT_BUSY", "sync_busy", nil)
        if data, err := c.uxCSyncCommand(context.Background()); err == nil || data != nil || syncer.Calls != 2 {
            t.Fatal(data, err, syncer)
        }
    }
    o := fixture(t)
    c := uxCCall(t, o, "en", "", "sync", "--dry-run")
    data, err := c.uxCSyncCommand(context.Background())
    if err != nil || !data.(client.SyncResult).DryRun {
        t.Fatal(data, err)
    }
    if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
        t.Fatal("dry-run created state")
    }
}

func TestUXCMoreMenuExposesLocalSyncBeforeTransfer(t *testing.T) {
    o := fixture(t)
    syncer := &fakeSyncer{}
    o.Syncer = syncer
    c := uxCCall(t, o, "en", "1\n4\n", "sync")
    kind, err := c.uxMore(context.Background())
    if err != nil || kind != "sync" {
        t.Fatal("source inspection is unreachable", kind, err)
    }
    data, err := c.uxNavigate(context.Background(), uxRoute{Kind: kind})
    if err != nil || data != nil || syncer.Calls != 0 {
        t.Fatal("menu preview performed a transfer", data, err)
    }
    if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
        t.Fatal("menu preview created state")
    }
}
