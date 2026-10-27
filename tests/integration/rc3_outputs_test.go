package integration

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    execution "github.com/elebirds/loaf/internal/run"
)

func TestPublicOutputInventoryAndBatchPull(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    first := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", "mkdir -p outputs/sub/deep; printf first >outputs/中文; printf nested >outputs/sub/deep/file")
    second := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", "mkdir -p outputs; printf second >outputs/中文")
    before := f.remoteRecord(first.RemoteDir, first.ID)
    code, out := cli(t, project, "outputs", first.ID, "--depth", "1", "--limit", "200", "--json")
    var listing struct {
        Data execution.OutputListing `json:"data"`
    }
    if code != 0 || json.Unmarshal(out, &listing) != nil || !listing.Data.Truncated || listing.Data.DepthLimited == 0 {
        t.Fatal(code, string(out))
    }
    // Removing only the isolated fixture helper must not trigger an implicit upload.
    f.shell(`rm "$HOME/.local/share/loaf/bin/"*/loaf-runner`)
    code, out = cli(t, project, "outputs", first.ID, "--json")
    if code != 1 || !strings.Contains(string(out), `"code":"RUNNER_REQUIRED"`) {
        t.Fatal(code, string(out))
    }
    code, out = cli(t, project, "outputs", first.ID, "--prepare-runner", "--json")
    if code != 0 {
        t.Fatal(code, string(out))
    }
    after := f.remoteRecord(first.RemoteDir, first.ID)
    if string(before) != string(after) {
        t.Fatal("inventory changed record")
    }
    destination := filepath.Join(project, "comparison")
    code, out = cli(t, project, "pull", first.ID, second.ID, "--into", destination, "--concurrency", "2", "--json")
    if code != 0 || !strings.Contains(string(out), `"completed":2`) {
        t.Fatal(code, string(out))
    }
    for _, row := range []struct{ id, text string }{{first.ID, "first"}, {second.ID, "second"}} {
        data, err := os.ReadFile(filepath.Join(destination, row.id, "outputs/中文"))
        if err != nil || string(data) != row.text {
            t.Fatal(string(data), err)
        }
    }
}
