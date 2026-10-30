package integration

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    execution "github.com/elebirds/loaf/internal/run"
    sources "github.com/elebirds/loaf/internal/sync"
)

// Run through the single shared build/integration owner. Every path, key,
// process and output in this test belongs to the temporary SSH fixture.
func TestUXFNativeLiteralSelectionAndPartialBatch(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    sourceFile(t, project, "loaf.toml", "[outputs]\npaths=['outputs','missing']\n[pull]\nchecksum=true\n")
    first := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs/nested/deeper; printf first >'outputs/模型 [1]*?,.txt'; printf deep >outputs/nested/deeper/file; ln -s '模型 [1]*?,.txt' outputs/latest`)
    second := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs; printf second >outputs/other`)
    code, out := cli(t, project, "outputs", first.ID, "--depth", "1", "--limit", "200", "--json")
    var listing struct {
        Data execution.OutputListing `json:"data"`
    }
    if code != 1 || json.Unmarshal(out, &listing) != nil || !listing.Data.Truncated || listing.Data.DepthLimited == 0 || len(listing.Data.Missing) != 1 || !strings.Contains(string(out), `"code":"OUTPUT_MISSING"`) {
        t.Fatal(code, string(out))
    }
    // A changed current alias must not redirect either download.
    if code, out := cli(t, project, "machine", "add", "other", "--ssh", "ssh -p 9 nobody@127.0.0.1", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    if code, out := cli(t, project, "machine", "use", "other", "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    exact := filepath.Join(project, "exact export")
    sourceFile(t, exact, "keep.txt", "local extra")
    one := pullCLI(t, project, 1, first.ID, "--path", "outputs/模型 [1]*?,.txt", "--path", "missing", "--to", exact, "--checksum=false", "--json")
    canonical, _ := filepath.EvalSymlinks(exact)
    if one.Destination != canonical || one.Policy.Checksum || len(one.DownloadedPaths) != 1 || len(one.MissingPaths) != 1 || one.ChangedFiles == nil {
        t.Fatal(one)
    }
    if data, err := os.ReadFile(filepath.Join(exact, "outputs/模型 [1]*?,.txt")); err != nil || string(data) != "first" {
        t.Fatal(string(data), err)
    }
    if data, err := os.ReadFile(filepath.Join(exact, "keep.txt")); err != nil || string(data) != "local extra" {
        t.Fatal(string(data), err)
    }
    into := filepath.Join(project, "batch export")
    code, out = cli(t, project, "pull", first.ID, second.ID, "--path", "outputs/模型 [1]*?,.txt", "--into", into, "--checksum=false", "--json")
    var batch struct {
        Data struct {
            Completed, Failed, Unexamined int
            Runs                          []struct {
                Result sources.PullResult `json:"result"`
                Error  *struct {
                    Code string `json:"code"`
                }   `json:"error"`
            }   `json:"runs"`
        } `json:"data"`
    }
    if code != 1 || json.Unmarshal(out, &batch) != nil || batch.Data.Completed != 1 || batch.Data.Failed != 1 || batch.Data.Unexamined != 0 || len(batch.Data.Runs) != 2 {
        t.Fatal(code, string(out))
    }
    if batch.Data.Runs[1].Error == nil || batch.Data.Runs[1].Error.Code != "OUTPUT_MISSING" || batch.Data.Runs[1].Result.ChangedFiles != nil {
        t.Fatal(string(out))
    }
    if data, err := os.ReadFile(filepath.Join(into, first.ID, "outputs/模型 [1]*?,.txt")); err != nil || string(data) != "first" {
        t.Fatal(string(data), err)
    }
}
