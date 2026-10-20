package integration

import (
    "context"
    "os"
    "os/exec"
    "testing"
    "time"

    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/wire"
)

func TestRunnerStopGraceAndGroupCleanup(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh for isolated SSH tests")
    }
    f := newFixture(t)
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    report, err := f.client.Preflight(ctx, f.target, "~/stopping", true)
    if err != nil {
        t.Fatal(err)
    }
    info, err := (execution.Bootstrap{Remote: f.client}).Verify(ctx, f.target, report.Arch, true)
    if err != nil {
        t.Fatal(err)
    }
    root := "/home/researcher/stopping 中文"
    f.shell(`mkdir -p "$1/cache"; printf 'project\n' >"$1/.loaf-project"`, root)
    invoke := func(action, id string, grace *int64, script string) *wire.RunRecord {
        t.Helper()
        req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: action, Root: root, ProjectID: "project", RunID: id, GraceMillis: grace}
        if script != "" {
            req.Args = []string{"/bin/sh", "-c", script}
        }
        r, err := f.client.Control(ctx, f.target, info.Path, req, true)
        if err != nil {
            t.Fatal(action, id, err)
        }
        return r
    }
    ready := func(id, file string) {
        t.Helper()
        wait(t, 5*time.Second, func() bool {
            return exec.CommandContext(ctx, "docker", "exec", f.id, "test", "-s", root+"/runs/"+id+"/work/"+file).Run() == nil
        })
    }
    finished := func(id string) *wire.RunRecord {
        t.Helper()
        var r *wire.RunRecord
        wait(t, 5*time.Second, func() bool {
            r = invoke("status", id, nil, "")
            return wire.Terminal(r.Status)
        })
        return r
    }

    invoke("start", "cleanup", nil, `trap 'sleep .4; echo cleaned >cleaned; exit 23' TERM; echo ready >ready; while :; do sleep .02; done`)
    ready("cleanup", "ready")
    grace := int64(1000)
    first := invoke("stop", "cleanup", &grace, "")
    zero := int64(0)
    duplicate := invoke("stop", "cleanup", &zero, "")
    if first.StopGraceMillis == nil || duplicate.StopGraceMillis == nil || *first.StopGraceMillis != grace || *duplicate.StopGraceMillis != grace {
        t.Fatal("duplicate stop changed grace", first, duplicate)
    }
    r := finished("cleanup")
    if r.Status != "stopped" || r.ExitCode == nil || *r.ExitCode != 23 || r.Signal != "" {
        t.Fatalf("cleanup did not complete in grace: %+v", r)
    }
    f.shell(`test -s "$1/runs/cleanup/work/cleaned"`, root)
    before := f.shell(`cat "$1/runs/cleanup/run.json"`, root)
    invoke("stop", "cleanup", &zero, "")
    if after := f.shell(`cat "$1/runs/cleanup/run.json"`, root); after != before {
        t.Fatal("duplicate stop rewrote terminal result")
    }

    command(t, "docker", "exec", "-d", "--user", "researcher", f.id, "/bin/sh", "-c", `echo $$ >"$1/sentinel.pid"; exec sleep 60`, "--", root)
    invoke("start", "children", nil, `trap 'exit 0' TERM; sh -c 'trap "" TERM; echo $$ >worker.pid; while :; do sleep .05; done' & wait`)
    ready("children", "worker.pid")
    grace = 100
    invoke("stop", "children", &grace, "")
    r = finished("children")
    if r.Status != "stopped" || r.ExitCode == nil || *r.ExitCode != 0 {
        t.Fatalf("lost leader result: %+v", r)
    }
    f.shell(`worker=$(cat "$1/runs/children/work/worker.pid"); if [ -e "/proc/$worker/stat" ]; then test "$(awk '{print $3}' "/proc/$worker/stat")" = Z; fi; kill -0 "$(cat "$1/sentinel.pid")"`, root)
}
