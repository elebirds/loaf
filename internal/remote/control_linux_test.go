package remote

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/wire"
)

func managedRoot(t *testing.T) string {
    t.Helper()
    root := t.TempDir()
    if err := os.WriteFile(filepath.Join(root, ".loaf-project"), []byte("project"), 0600); err != nil {
        t.Fatal(err)
    }
    return root
}

func TestOwnershipAndLostSupervisor(t *testing.T) {
    root := managedRoot(t)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "status", Root: root, ProjectID: "project", RunID: "run"}
    dir, err := runPath(req)
    if err != nil {
        t.Fatal(err)
    }
    if err := os.Mkdir(dir, 0700); err != nil {
        t.Fatal(err)
    }
    r := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", CreatedAt: time.Now(), SupervisorPID: os.Getpid(), SupervisorStart: "wrong", BootID: "wrong"}
    if err := save(dir, r); err != nil {
        t.Fatal(err)
    }
    r, err = Handle(req)
    if err != nil || r.Status != "unknown" || r.ExitCode != nil {
        t.Fatal(r, err)
    }
    req.Action = "stop"
    r, err = Handle(req)
    if err == nil || err.Error() != "STOP_UNCONFIRMED" || r.Status != "unknown" {
        t.Fatal(r, err)
    }
    if _, err := os.Stat(filepath.Join(dir, "stop.request")); !os.IsNotExist(err) {
        t.Fatal("blind stop requested")
    }
    req.Root = "/"
    if _, err := Handle(req); err == nil {
        t.Fatal("accepted root")
    }
}

func TestTerminalAndProtocol(t *testing.T) {
    root := managedRoot(t)
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Root: root, ProjectID: "project", RunID: "run"}
    dir, _ := runPath(req)
    if err := os.Mkdir(dir, 0700); err != nil {
        t.Fatal(err)
    }
    zero := 0
    r := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "succeeded", ExitCode: &zero}
    if err := save(dir, r); err != nil {
        t.Fatal(err)
    }
    req.Action = "stop"
    r, err := Handle(req)
    if err != nil || r.Status != "succeeded" || *r.ExitCode != 0 {
        t.Fatal(r, err)
    }
    for _, input := range []string{`not json`, `{} {}`, `{"format_version":99}`} {
        var out bytes.Buffer
        if Serve(strings.NewReader(input), &out) == 0 || !strings.Contains(out.String(), `"ok":false`) {
            t.Fatal(input, out.String())
        }
    }
    p, err := process(os.Getpid())
    if err != nil || p.start == "" || p.group < 1 {
        t.Fatal(p, err)
    }
}
