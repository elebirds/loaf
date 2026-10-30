package integration

import (
    "bytes"
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/privatefs"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
)

func TestWSLMountedProjectWithLinuxPrivateState(t *testing.T) {
    base := os.Getenv("LOAF_WSL_PROJECT_BASE")
    if os.Getenv("LOAF_INTEGRATION") != "1" || base == "" {
        t.Skip("set LOAF_WSL_PROJECT_BASE to a /mnt/c test directory in WSL2")
    }
    kernel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
    if !strings.Contains(strings.ToLower(string(kernel)), "microsoft") || !strings.HasPrefix(base, "/mnt/c/") {
        t.Fatal("requires an actual WSL mounted Windows project")
    }
    project, err := os.MkdirTemp(base, "loaf-wsl-")
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { os.RemoveAll(project) })
    command(t, "git", "-C", project, "init", "-q")
    sourceFile(t, project, "loaf.toml", "[outputs]\npaths=['outputs']\n")
    sourceFile(t, project, "源文件 space.txt", "mounted source")
    state := t.TempDir()
    f := newFixture(t)
    invoke := func(args ...string) []byte {
        t.Helper()
        cmd := exec.Command(os.Getenv("LOAF_BIN"), append(args, "--json")...)
        cmd.Dir = project
        cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(state, "config"), "XDG_STATE_HOME="+filepath.Join(state, "state"), "LOAF_LANG=en")
        var diagnostic bytes.Buffer
        cmd.Stderr = &diagnostic
        output, err := cmd.Output()
        if err != nil || diagnostic.Len() != 0 || !json.Valid(output) {
            t.Fatal(args, err, string(output), diagnostic.String())
        }
        return output
    }
    invoke("machine", "add", "wsl", "--ssh", "ssh -F "+ssh.Quote(f.config)+" fixture")
    invoke("machine", "use", "wsl")
    invoke("check")
    invoke("sync")
    // --json precedes -- so it cannot become part of the user's remote argv.
    cmd := exec.Command(os.Getenv("LOAF_BIN"), "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", `mkdir outputs; cat '源文件 space.txt' >'outputs/结果 space.txt'; echo wsl-mounted`)
    cmd.Dir = project
    cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(state, "config"), "XDG_STATE_HOME="+filepath.Join(state, "state"), "LOAF_LANG=en")
    out, err := cmd.CombinedOutput()
    var response struct {
        Data execution.View `json:"data"`
    }
    if err != nil || json.Unmarshal(out, &response) != nil || response.Data.Status != "succeeded" {
        t.Fatal(err, string(out))
    }
    id := response.Data.ID
    invoke("status", id)
    invoke("logs", id)
    invoke("pull", id)
    if data, err := os.ReadFile(filepath.Join(project, "loaf-results", id, "outputs", "结果 space.txt")); err != nil || string(data) != "mounted source" {
        t.Fatal("mounted output mismatch", err)
    }
    invoke("clean", id, "--yes")
    if err := privatefs.Check(filepath.Join(state, "state", "loaf", "state-v3", "state.db"), false); err != nil {
        t.Fatal("state is not private in Linux", err)
    }
    for _, name := range []string{"config", "state"} {
        if _, err := os.Stat(filepath.Join(project, name)); !os.IsNotExist(err) {
            t.Fatal("state leaked into mounted project", name, err)
        }
    }
}
