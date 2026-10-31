package integration

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/queue"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/wire"
)

// The two candidates use distinct software identities and the same supported
// first-release formats. Once 1.0 ships, OLD_BIN is its actual release artifact.
func compatibilityCLI(t *testing.T, binary, project string, args ...string) (int, []byte) {
    t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, binary, args...)
    cmd.Dir = project
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(project, "config"), "XDG_STATE_HOME="+filepath.Join(project, "state"), "LOAF_LANG=en")
    var diagnostic bytes.Buffer
    cmd.Stderr = &diagnostic
    out, err := cmd.Output()
    code := 0
    if err != nil {
        var exited *exec.ExitError
        if !errors.As(err, &exited) || ctx.Err() != nil {
            t.Fatal(args, err, string(out), diagnostic.String())
        }
        code = exited.ExitCode()
    }
    if diagnostic.Len() != 0 {
        t.Fatal("JSON invocation emitted diagnostics", args, diagnostic.String())
    }
    return code, out
}

func compatibilityData[T any](t *testing.T, binary, project string, want int, args ...string) T {
    t.Helper()
    code, out := compatibilityCLI(t, binary, project, args...)
    var result struct {
        Data T `json:"data"`
    }
    if code != want || json.Unmarshal(out, &result) != nil {
        t.Fatalf("%s %v: exit=%d want=%d %s", filepath.Base(binary), args, code, want, out)
    }
    return result.Data
}

func compatibilityProject(t *testing.T, f *fixture, binary string) string {
    t.Helper()
    project := t.TempDir()
    for _, args := range [][]string{{"machine", "add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"machine", "use", "lab", "--json"}} {
        compatibilityData[any](t, binary, project, 0, args...)
    }
    sourceFile(t, project, "loaf.toml", "[outputs]\npaths=['outputs']\n")
    return project
}

func TestPackagedV1Compatibility(t *testing.T) {
    old := os.Getenv("LOAF_COMPAT_OLD_BIN")
    current := os.Getenv("LOAF_BIN")
    if os.Getenv("LOAF_INTEGRATION") != "1" || old == "" {
        t.Skip("set LOAF_COMPAT_OLD_BIN to the v1.0.0 candidate/release and run scripts/integration.sh with the newer candidate")
    }
    type identity struct {
        Version string `json:"version"`
    }
    versionDir := t.TempDir()
    before := compatibilityData[identity](t, old, versionDir, 0, "version", "--json")
    after := compatibilityData[identity](t, current, versionDir, 0, "version", "--json")
    if before.Version != "v1.0.0" || !wire.StableV1(after.Version) || before.Version == after.Version || after.Version != wire.Version {
        t.Fatal("requires distinct formal 1.x artifacts", before, after, wire.Version)
    }
    f := newFixture(t)
    project := compatibilityProject(t, f, old)

    t.Run("run-cursor-stop-clean-abandon", func(t *testing.T) {
        started := compatibilityData[execution.View](t, old, project, 0, "run", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs; printf 'before-upgrade\n'; printf original >outputs/result.txt; printf ready >ready; while [ ! -f release ]; do sleep .03; done; printf 'after-upgrade\n'`)
        waitRunFile(t, f, started, "work/ready")
        logs := compatibilityData[execution.View](t, old, project, 0, "logs", started.ID, "--json")
        if logs.Log == nil || logs.Log.Cursor == "" || !strings.Contains(logs.Log.Text, "before-upgrade") {
            t.Fatal(logs)
        }
        cursor, err := wire.DecodeLogCursor(logs.Log.Cursor)
        if err != nil || cursor.Version != before.Version {
            t.Fatal(cursor, err)
        }
        supervisor := f.remoteSQL(started.RemoteDir, "SELECT supervisor_pid FROM runs WHERE run_id='"+started.ID+"'")
        inspected := compatibilityData[execution.LocalDetails](t, current, project, 0, "inspect", started.ID, "--json")
        live := compatibilityData[execution.View](t, current, project, 0, "status", started.ID, "--json")
        if !inspected.Compatible || inspected.Version != before.Version || inspected.ClientVersion != after.Version || live.Version != before.Version || live.ID != started.ID {
            t.Fatal(inspected, live)
        }
        if got := f.remoteSQL(started.RemoteDir, "SELECT supervisor_pid FROM runs WHERE run_id='"+started.ID+"'"); got != supervisor {
            t.Fatal("client update replaced the Supervisor", supervisor, got)
        }
        f.shell(`touch "$1/runs/$2/work/release"`, started.RemoteDir, started.ID)
        code, stream := compatibilityCLI(t, current, project, "logs", started.ID, "-f", "--resume", logs.Log.Cursor, "--json")
        if code != 0 {
            t.Fatal(code, string(stream))
        }
        decoder := json.NewDecoder(bytes.NewReader(stream))
        text, ends := "", 0
        for decoder.More() {
            var item struct {
                Data struct {
                    Type string         `json:"type"`
                    Run  execution.View `json:"run"`
                } `json:"data"`
            }
            if err := decoder.Decode(&item); err != nil || item.Data.Run.Version != before.Version {
                t.Fatal(item, err)
            }
            if item.Data.Run.Log != nil {
                text += item.Data.Run.Log.Text
            }
            if item.Data.Type == "end" {
                ends++
            }
        }
        if ends != 1 || text != "after-upgrade\n" {
            t.Fatal("old cursor replayed or skipped bytes", ends, text, string(stream))
        }
        pulled := compatibilityData[sources.PullResult](t, current, project, 0, "pull", started.ID, "--json")
        if data, err := os.ReadFile(filepath.Join(pulled.Destination, "outputs", "result.txt")); err != nil || string(data) != "original" {
            t.Fatal(string(data), err)
        }
        compatibilityData[any](t, current, project, 0, "clean", started.ID, "--yes", "--json")
        kept := compatibilityData[execution.LocalDetails](t, current, project, 0, "inspect", started.ID, "--json")
        if kept.Version != before.Version || kept.CleanedAt == nil {
            t.Fatal("cleanup lost old origin or completion", kept)
        }
        stopping := compatibilityData[execution.View](t, old, project, 0, "run", "--json", "--", "/bin/sh", "-c", `printf ready >ready; while :; do sleep .05; done`)
        waitRunFile(t, f, stopping, "work/ready")
        stopped := compatibilityData[execution.View](t, current, project, 0, "stop", stopping.ID, "--grace", "1s", "--json")
        if stopped.ID != stopping.ID || stopped.Version != before.Version || stopped.Status != "stopped" {
            t.Fatal(stopped)
        }

        // Real copy failure leaves an old preparation, not an executed Run.
        sourceFile(t, project, "loaf.toml", "[sync]\nmode='update'\n[outputs]\npaths=['outputs']\n")
        compatibilityData[any](t, old, project, 0, "sync", "--json")
        f.shell(`mkfifo "$1/cache/zz-v1-unsupported"`, stopping.RemoteDir)
        prepared := compatibilityData[execution.View](t, old, project, 1, "run", "--json", "--", "/bin/true")
        if prepared.ID == "" || prepared.Submission != "preparing" || prepared.Version != before.Version {
            t.Fatal(prepared)
        }
        compatibilityData[client.AbandonBatch](t, current, project, 0, "abandon", prepared.ID, "--dry-run", "--json")
        abandoned := compatibilityData[client.AbandonBatch](t, current, project, 0, "abandon", prepared.ID, "--yes", "--json")
        if len(abandoned.Runs) != 1 || abandoned.Runs[0].NoStartAt == nil || abandoned.Runs[0].CleanedAt == nil || abandoned.Runs[0].Run.Version != before.Version {
            t.Fatal(abandoned)
        }
        f.shell(`rm "$1/cache/zz-v1-unsupported"`, stopping.RemoteDir)
    })

    t.Run("notification-state-and-dedup", func(t *testing.T) {
        notificationProject := compatibilityProject(t, f, old)
        var mu sync.Mutex
        accepted, uncertain := 0, 0
        receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            io.Copy(io.Discard, r.Body)
            mu.Lock()
            if r.URL.Path == "/uncertain" {
                uncertain++
            } else {
                accepted++
            }
            mu.Unlock()
            if r.URL.Path == "/uncertain" {
                connection, _, err := w.(http.Hijacker).Hijack()
                if err == nil {
                    connection.Close()
                }
                return
            }
            w.WriteHeader(http.StatusAccepted)
        }))
        defer receiver.Close()
        for _, name := range []string{"accepted", "uncertain"} {
            compatibilityData[client.DestinationRecord](t, old, notificationProject, 0, "notify", "destination", "add", name, "--kind", "webhook", "--endpoint", receiver.URL+"/"+name, "--json")
        }
        first := compatibilityData[execution.View](t, old, notificationProject, 0, "run", "--notify", "accepted", "--notify", "uncertain", "--wait", "5s", "--json", "--", "/bin/true")
        if first.Status != "succeeded" {
            t.Fatal(first)
        }
        compatibilityData[client.NotifyServeResult](t, old, notificationProject, 3, "notify", "serve", "--once", "--json")
        // A second original-version intent has not yet been collected or sent.
        second := compatibilityData[execution.View](t, old, notificationProject, 0, "run", "--notify", "accepted", "--wait", "5s", "--json", "--", "/bin/true")
        if second.Status != "succeeded" {
            t.Fatal(second)
        }
        result := compatibilityData[client.NotifyServeResult](t, current, notificationProject, 3, "notify", "serve", "--once", "--json")
        if result.Attempted != 1 || result.Accepted != 1 || result.Unconfirmed != 1 {
            t.Fatal("upgrade lost pending/unconfirmed state", result)
        }
        replay := compatibilityData[client.NotifyServeResult](t, current, notificationProject, 3, "notify", "serve", "--once", "--json")
        if replay.Attempted != 0 || replay.Unconfirmed != 1 {
            t.Fatal("upgrade replayed accepted or ambiguous sends", replay)
        }
        mu.Lock()
        defer mu.Unlock()
        if accepted != 2 || uncertain != 1 {
            t.Fatal("source cursor or dedup was reset", accepted, uncertain)
        }
    })

    t.Run("old-coordinator-new-admission", func(t *testing.T) {
        firstProject := compatibilityProject(t, f, old)
        secondProject := compatibilityProject(t, f, current)
        enabled := compatibilityData[queue.Result](t, old, firstProject, 0, "queue", "enable", "--slots", "1", "--json")
        if enabled.Status == nil || !enabled.Status.CoordinatorRunning {
            t.Fatal(enabled)
        }
        coordinator := f.shell(`pgrep -f "^/home/researcher/.local/share/loaf/bin/$1/loaf-runner __coordinate "`, before.Version)
        first := compatibilityData[client.QueueSubmitResult](t, old, firstProject, 0, "queue", "submit", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs; while [ ! -f /tmp/loaf-v1-queue-release ]; do sleep .03; done; printf old >outputs/result.txt`)
        if first.Request == nil {
            t.Fatal(first)
        }
        sourceFile(t, secondProject, "input.txt", "frozen-new-input")
        second := compatibilityData[client.QueueSubmitResult](t, current, secondProject, 0, "queue", "submit", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs; cat input.txt >outputs/result.txt`)
        if second.Request == nil || second.Request.TargetID != first.Request.TargetID || second.Request.Sequence <= first.Request.Sequence {
            t.Fatal("upgrade created a second capacity ledger", first, second)
        }
        waiting := compatibilityData[queue.Result](t, current, secondProject, 0, "queue", "show", second.Request.ID, "--json")
        if waiting.Request == nil || waiting.Request.State != "queued" {
            t.Fatal("new submission bypassed old Coordinator capacity", waiting)
        }
        sourceFile(t, secondProject, "input.txt", "later-edit")
        compatibilityData[any](t, current, secondProject, 0, "sync", "--json")
        canceled := compatibilityData[client.QueueSubmitResult](t, old, firstProject, 0, "queue", "submit", "--json", "--", "/bin/true")
        if canceled.Request == nil {
            t.Fatal(canceled)
        }
        compatibilityData[queue.Result](t, current, firstProject, 0, "queue", "cancel", canceled.Request.ID, "--json")
        compatibilityData[any](t, current, firstProject, 0, "queue", "clean", canceled.Request.ID, "--yes", "--json")
        f.shell(`touch /tmp/loaf-v1-queue-release`)
        wait(t, 20*time.Second, func() bool {
            result := compatibilityData[execution.View](t, current, secondProject, 0, "status", second.Request.RunID, "--json")
            return result.Status == "succeeded"
        })
        oldResult := compatibilityData[queue.Result](t, current, firstProject, 0, "queue", "show", first.Request.ID, "--json")
        if oldResult.Request == nil || oldResult.Request.RunID != first.Request.RunID {
            t.Fatal("upgrade replaced the original queued Run", oldResult)
        }
        pulled := compatibilityData[sources.PullResult](t, current, secondProject, 0, "pull", second.Request.RunID, "--json")
        if data, err := os.ReadFile(filepath.Join(pulled.Destination, "outputs", "result.txt")); err != nil || string(data) != "frozen-new-input" {
            t.Fatal("old Coordinator lost the new frozen input", string(data), err)
        }
        if got := f.shell(`pgrep -f "^/home/researcher/.local/share/loaf/bin/$1/loaf-runner __coordinate "`, before.Version); got != coordinator {
            t.Fatal("new client restarted the old Coordinator", coordinator, got)
        }
    })
}
