package integration

import (
    "bytes"
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/wire"
)

func TestPublicInspectionNamesTraceAndCursor(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := t.TempDir()
    sourceFile(t, project, "loaf.toml", "[run]\nwait = '5s'\n")
    for _, args := range [][]string{{"add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"use", "lab", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(string(out))
        }
    }
    code, out := cli(t, project, "run", "--name", "seed A 中文", "--tag", "ab", "--tag", "seed1", "--trace", "--json", "--", "/bin/sh", "-c", "printf 'one\\nsecond 中文\\n'")
    var accepted struct {
        Data  execution.View   `json:"data"`
        Trace *ssh.TraceReport `json:"trace"`
    }
    if code != 0 || json.Unmarshal(out, &accepted) != nil || accepted.Data.Name != "seed A 中文" || accepted.Data.Version != wire.Version || accepted.Data.Log == nil || accepted.Data.Log.Cursor == "" || accepted.Trace == nil || len(accepted.Trace.Stages) == 0 {
        t.Fatal(code, string(out))
    }
    for _, args := range [][]string{{"inspect", accepted.Data.ID, "--json"}, {"ps", accepted.Data.ID, "--local", "--json"}, {"ps", "--name", "seed A 中文", "--tag", "ab", "--tag", "seed1", "--limit", "1", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 || !bytes.Contains(out, []byte(accepted.Data.ID)) || !bytes.Contains(out, []byte("seed A 中文")) {
            t.Fatal(code, string(out))
        }
    }
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    cmd := stopCLICommand(ctx, project, "logs", accepted.Data.ID, "-f", "--resume", accepted.Data.Log.Cursor, "--json")
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    streamed, err := cmd.Output()
    if err != nil || stderr.Len() != 0 {
        t.Fatal(string(streamed), stderr.String(), err)
    }
    decoder := json.NewDecoder(bytes.NewReader(streamed))
    ends := 0
    text := ""
    for decoder.More() {
        var event struct {
            Data struct {
                Type string         `json:"type"`
                Run  execution.View `json:"run"`
            } `json:"data"`
        }
        if err := decoder.Decode(&event); err != nil {
            t.Fatal(err)
        }
        if event.Data.Type == "end" {
            ends++
        }
        if event.Data.Run.Log != nil {
            text += event.Data.Run.Log.Text
        }
    }
    if ends != 1 || text != "" {
        t.Fatal("resumed EOF replayed text", string(streamed))
    }
    // Removing the alias cannot remove offline access or reinterpret binding.
    if code, out := cli(t, project, "remove", "lab", "--json"); code != 0 {
        t.Fatal(string(out))
    }
    if code, out := cli(t, project, "inspect", accepted.Data.ID, "--json"); code != 0 || !bytes.Contains(out, []byte(`"source":"local"`)) {
        t.Fatal(code, string(out))
    }
    state := local.New(filepath.Join(project, "state", "loaf"))
    database, err := db.Open(ctx, state.Path, false)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    var locator []byte
    err = database.QueryRow("SELECT data FROM runs WHERE id=?", accepted.Data.ID).Scan(&locator)
    if err != nil || strings.Contains(string(locator), "seed A 中文") {
        t.Fatal(string(locator), err)
    }
}
