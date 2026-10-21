package integration

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"

    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

// Exercise the feature boundaries together: machine aliases are preferences,
// while existing runs keep their own connections and cleanup availability.
func TestDailyWorkflowBoundRunsAndCleanup(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    sourceFile(t, project, "loaf.toml", "[run]\nwait = '5s'\n")
    foreground := exec.Command(os.Getenv("LOAF_BIN"), "exec", "--", "printf", "%s", "machine-ready")
    foreground.Dir = project
    foreground.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(project, "config"), "XDG_STATE_HOME="+filepath.Join(project, "state"), "LOAF_LANG=en")
    if out, err := foreground.CombinedOutput(); err != nil || string(out) != "machine-ready" {
        t.Fatal("foreground command", string(out), err)
    }
    older := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "echo older; exit 7")
    latest := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", "mkdir outputs; printf collected >outputs/result.txt")
    if older.Status != "failed" || latest.Status != "succeeded" {
        t.Fatal(older, latest)
    }
    // Queries repair the current helper without mutating the saved run outcome.
    f.shell(`rm "$HOME/.local/share/loaf/bin/$1/loaf-runner"`, wire.Version)
    if observed := runCLI(t, project, "ps", older.ID, "--json"); observed.Status != "failed" {
        t.Fatal("missing runner was not repaired", observed)
    }
    mismatched := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"format_version\":99,\"version\":\"wrong-version\",\"ok\":true}'\n"
    f.shell(`set -eu; target="$HOME/.local/share/loaf/bin/$1/loaf-runner"; printf '%s' "$2" >"$target.replacement"; chmod 700 "$target.replacement"; mv "$target.replacement" "$target"`, wire.Version, mismatched)
    if observed := runCLI(t, project, "ps", latest.ID, "--json"); observed.Status != "succeeded" {
        t.Fatal("mismatched runner was not updated", observed)
    }
    downloaded := pullCLI(t, project, 0, latest.ID, "--json")
    resultPath := filepath.Join(downloaded.Destination, "outputs", "result.txt")
    if data, err := os.ReadFile(resultPath); err != nil || string(data) != "collected" {
        t.Fatal(string(data), err)
    }
    for _, args := range [][]string{
        {"add", "other", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--remote-dir", "~/other-projects", "--json"},
        {"use", "other", "--json"},
        {"remove", "lab", "--json"},
    } {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(args, code, string(out))
        }
    }
    // A removed alias still identifies the original bound historical runs.
    code, out := cli(t, project, "ps", "--target", "lab", "--status", "succeeded", "--json")
    var list struct {
        Data struct {
            Runs []execution.View `json:"runs"`
        } `json:"data"`
    }
    if code != 0 || json.Unmarshal(out, &list) != nil || len(list.Data.Runs) != 1 || list.Data.Runs[0].ID != latest.ID {
        t.Fatal(code, string(out))
    }
    if code, out := cli(t, project, "clean", latest.ID, "--dry-run", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    f.shell(`set -eu; test -d "$1/runs/$2/work"; mkdir -p /home/researcher/unrelated; printf keep >/home/researcher/unrelated/data`, latest.RemoteDir, latest.ID)
    for i := 0; i < 2; i++ {
        if code, out := cli(t, project, "clean", latest.ID, "--yes", "--json"); code != 0 {
            t.Fatal("repeatable explicit cleanup", code, string(out))
        }
    }
    observed := runCLI(t, project, "ps", latest.ID, "--json")
    if observed.Status != "succeeded" || observed.Target != "lab" || observed.ExitCode == nil || *observed.ExitCode != 0 || observed.CleanedAt == nil || observed.CleanupPending {
        t.Fatal("retained execution summary", observed)
    }
    for _, command := range []string{"logs", "pull"} {
        if code, out := cli(t, project, command, "--json"); code != 1 || !strings.Contains(string(out), `"code":"RUN_CLEANED"`) {
            t.Fatal("latest must not fall back", command, code, string(out))
        }
    }
    preserved := runCLI(t, project, "ps", older.ID, "--json")
    if preserved.Status != "failed" || preserved.ExitCode == nil || *preserved.ExitCode != 7 {
        t.Fatal("other run changed", preserved)
    }
    if data, err := os.ReadFile(resultPath); err != nil || string(data) != "collected" {
        t.Fatal("local results changed", string(data), err)
    }
    f.shell(`set -eu; test ! -e "$1/runs/$2"; test -d "$1/runs/$3/work"; test "$(cat /home/researcher/unrelated/data)" = keep`, latest.RemoteDir, latest.ID, older.ID)
}
