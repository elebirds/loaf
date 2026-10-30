package integration

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "os"
    "path"
    "path/filepath"
    "testing"
    "time"

    execution "github.com/elebirds/loaf/internal/run"
)

type publicLogFrame struct {
    OK   bool `json:"ok"`
    Data struct {
        Type string         `json:"type"`
        Run  execution.View `json:"run"`
    }   `json:"data"`
    Error *struct {
        Code string `json:"code"`
    }   `json:"error"`
}

func TestPublicLogCursorAcrossProcessesAndChangedLogs(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := stopProject(t, f)
    first, second := "first 中文\n", "second 中文\n"
    script := `mkdir outputs; echo once >outputs/launch-count; printf 'first 中文\n'; i=0; while [ ! -f release ]; do i=$((i+1)); test "$i" -lt 400 || exit 90; sleep .05; done; printf 'second 中文\n'`
    submitted := runCLI(t, project, "run", "--json", "--", "/bin/sh", "-c", script)
    ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
    defer cancel()

    // Consume one confirmed chunk, then cancel this CLI process. The next
    // invocation must use its cursor, not replay the initial tail or execution.
    checkpoint := filepath.Join(t.TempDir(), "cursor.json")
    original := stopCLICommand(ctx, project, "logs", submitted.ID, "-f", "--resume-file", checkpoint, "--json")
    output, err := original.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    var originalDiagnostic bytes.Buffer
    original.Stderr = &originalDiagnostic
    if err := original.Start(); err != nil {
        t.Fatal(err)
    }
    scanner := bufio.NewScanner(output)
    scanner.Buffer(make([]byte, 1024), 2<<20)
    cursor := ""
    for scanner.Scan() {
        var frame publicLogFrame
        if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil || !frame.OK || frame.Data.Run.Log == nil {
            t.Fatal(string(scanner.Bytes()), err)
        }
        if frame.Data.Run.Log.Text == "" {
            continue
        }
        log := frame.Data.Run.Log
        if log.Text != first || log.Offset != int64(len(first)) || log.Cursor == "" {
            t.Fatal(log)
        }
        cursor = log.Cursor
        break
    }
    if cursor == "" {
        t.Fatal("no confirmed initial cursor", scanner.Err())
    }
    if err := interruptCommand(t, original); err != nil {
        t.Fatal(err)
    }
    canceled := false
    for scanner.Scan() {
        var frame publicLogFrame
        if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
            t.Fatal(err)
        }
        canceled = canceled || frame.Error != nil && frame.Error.Code == "OBSERVATION_CANCELED"
    }
    err = original.Wait()
    if err == nil || scanner.Err() != nil || !canceled || originalDiagnostic.Len() != 0 {
        t.Fatal("cancellation did not finish the observing CLI", err, scanner.Err(), originalDiagnostic.String())
    }
    if current := runCLI(t, project, "status", submitted.ID, "--json"); current.Status != "running" {
        t.Fatal("canceling logs stopped the accepted task", current)
    }

    if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
        t.Fatal("JSON output auto-acknowledged processing", err)
    }
    if code, out := cli(t, project, "logs", submitted.ID, "--resume-file", checkpoint, "--ack-cursor", cursor, "--json"); code != 0 {
        t.Fatal(code, string(out))
    }
    savedCheckpoint, _ := os.ReadFile(checkpoint)
    resumed := stopCLICommand(ctx, project, "logs", submitted.ID, "-f", "--resume-file", checkpoint, "--json")
    output, err = resumed.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    var resumedDiagnostic bytes.Buffer
    resumed.Stderr = &resumedDiagnostic
    if err := resumed.Start(); err != nil {
        t.Fatal(err)
    }
    scanner = bufio.NewScanner(output)
    scanner.Buffer(make([]byte, 1024), 2<<20)
    if !scanner.Scan() {
        t.Fatal("resume did not observe the saved position", scanner.Err())
    }
    var ready publicLogFrame
    if err := json.Unmarshal(scanner.Bytes(), &ready); err != nil || !ready.OK || ready.Data.Run.Log == nil || ready.Data.Run.Log.Text != "" || ready.Data.Run.Log.Offset != int64(len(first)) {
        t.Fatal("new CLI replayed the confirmed tail", string(scanner.Bytes()), err)
    }
    // This initial observation, not a sleep, admits new output after resume.
    f.shell(`touch "$1/runs/$2/work/release"`, submitted.RemoteDir, submitted.ID)
    text, ends, offset := "", 0, int64(len(first))
    var terminal execution.View
    for scanner.Scan() {
        var frame publicLogFrame
        if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil || !frame.OK || frame.Data.Run.Log == nil {
            t.Fatal(string(scanner.Bytes()), err)
        }
        log := frame.Data.Run.Log
        text += log.Text
        offset += int64(len(log.Text))
        if log.Offset != offset || log.Cursor == "" {
            t.Fatal("cursor lost source bytes", log, offset)
        }
        if frame.Data.Type == "end" {
            ends++
            terminal = frame.Data.Run
        }
    }
    err = resumed.Wait()
    if err != nil || scanner.Err() != nil || resumedDiagnostic.Len() != 0 || text != second || ends != 1 || terminal.Status != "succeeded" {
        t.Fatal(text, ends, terminal, err, scanner.Err(), resumedDiagnostic.String())
    }
    afterCheckpoint, _ := os.ReadFile(checkpoint)
    if !bytes.Equal(savedCheckpoint, afterCheckpoint) {
        t.Fatal("piped JSON advanced acknowledged file")
    }
    whole := runCLI(t, project, "logs", submitted.ID, "--json")
    if whole.Log == nil || whole.Log.Text != first+second {
        t.Fatal("continuation did not match the complete source", whole)
    }
    f.shell(`test "$(wc -l <"$1/runs/$2/work/outputs/launch-count")" -eq 1`, submitted.RemoteDir, submitted.ID)

    logPath := path.Join(submitted.RemoteDir, "runs", submitted.ID, "output.log")
    for _, change := range []struct{ name, mutate, restore string }{
        {"same-inode-longer-rewrite", `cp -p "$1" "$1.saved"; dd if=/dev/zero of="$1" bs=4096 count=1 2>/dev/null`, `cat "$1.saved" >"$1"; cmp "$1" "$1.saved"; rm "$1.saved"`},
        {"replacement-with-identical-bytes", `mv "$1" "$1.saved"; cp -p "$1.saved" "$1"; cmp "$1" "$1.saved"`, `mv -f "$1.saved" "$1"`},
    } {
        t.Run(change.name, func(t *testing.T) {
            func() {
                f.shell("set -eu; "+change.mutate, logPath)
                defer f.shell("set -eu; "+change.restore, logPath)
                code, out := cliNDJSON(t, project, "logs", submitted.ID, "-f", "--resume", terminal.Log.Cursor, "--json")
                var frame publicLogFrame
                if code != 1 || json.Unmarshal(out, &frame) != nil || frame.Error == nil || frame.Error.Code != "LOG_CHANGED" {
                    t.Fatal("changed log silently reset the cursor", code, string(out))
                }
            }()
            code, out := cliNDJSON(t, project, "logs", submitted.ID, "-f", "--resume", terminal.Log.Cursor, "--json")
            decoder := json.NewDecoder(bytes.NewReader(out))
            ends := 0
            for decoder.More() {
                var frame publicLogFrame
                if err := decoder.Decode(&frame); err != nil || !frame.OK || frame.Data.Run.Log == nil || frame.Data.Run.Log.Text != "" {
                    t.Fatal("restored EOF cursor replayed output", code, string(out), err)
                }
                if frame.Data.Type == "end" {
                    ends++
                }
            }
            if code != 0 || ends != 1 {
                t.Fatal(code, ends, string(out))
            }
        })
    }
}
