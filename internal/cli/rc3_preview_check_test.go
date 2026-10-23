package cli

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
)

func TestRunPreviewIsFullyOfflineRedactedAndPreservesExplicitZero(t *testing.T) {
    o := fixture(t)
    code, out, stderr := run(t, o, "add", "lab", "--ssh", "host-secret", "--json")
    if code != 0 {
        t.Fatal(out, stderr)
    }
    global, _ := os.ReadFile(o.Store.ConfigFile)
    _ = os.WriteFile(o.Store.ConfigFile, append(global, []byte("\n[run]\nwait='30s'\n[sync]\nmode='update'\nrespect_gitignore=false\n[pull]\nchecksum=true\n")...), 0600)
    _ = os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("target='lab'\n[run]\ncommand='printf done'\nsetup='true'\n[run.env]\nPRIVATE='must-not-print-value'\n[outputs]\npaths=['custom']\n[pull]\nchecksum=false\n"), 0600)
    _ = os.WriteFile(filepath.Join(o.CWD, "actual.py"), []byte("print('x')"), 0600)
    _ = os.WriteFile(filepath.Join(o.CWD, ".env"), []byte("credential-secret"), 0600)
    o.Runs = nil
    o.Checker = nil
    code, out, stderr = run(t, o, "run", "--dry-run", "--wait", "0s", "--json")
    var r struct {
        Data execution.RunPreview `json:"data"`
    }
    if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &r) != nil || !r.Data.DryRun || r.Data.ExistingProjectID != "" || r.Data.WaitMillis != 0 || r.Data.Pull.Checksum || r.Data.Provenance["wait"][0] != "flag" || r.Data.Provenance["sync.mode"][0] != "user" || r.Data.Provenance["pull.checksum"][0] != "project" || r.Data.Source.Bytes <= 0 {
        t.Fatal(code, out, stderr)
    }
    for _, secret := range []string{"must-not-print-value", "credential-secret", "host-secret", "credential_ref", "run_id"} {
        if strings.Contains(out, secret) {
            t.Fatal("preview leaked", secret, out)
        }
    }
    if _, err := os.Stat(o.Store.IndexFile); !os.IsNotExist(err) {
        t.Fatal("preview created state", err)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr = run(t, o, "run", "--dry-run", "--lang", lang, "--", "echo", "literal")
        if code != 0 || stderr != "" || !strings.Contains(out, "literal") {
            t.Fatal(code, out, stderr)
        }
    }
    _ = os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("target='lab'"), 0600)
    code, out, stderr = run(t, o, "run", "--dry-run", "--json")
    if code != 2 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
}

type diagnosticChecker struct{}

func (diagnosticChecker) Check(_ context.Context, t config.Target, _ string, _ bool) (ssh.Report, error) {
    if t.Destination == "secret-host" {
        return ssh.Report{}, fault.At(fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Could not resolve hostname secret-host: credential-secret"}), "preflight")
    }
    return ssh.Report{OS: "Linux", Arch: "aarch64", RemoteDir: "/private/path-secret", GPU: "secret-model", Remote: map[string]ssh.Tool{"python": {Path: "/private/tool-secret", Version: "secret-version"}}}, nil
}
func TestMultiTargetCheckExportsAllowlistAndKeepsPartialResults(t *testing.T) {
    o := fixture(t)
    o.Checker = diagnosticChecker{}
    o.Trace = &ssh.Trace{}
    for _, name := range []string{"ok", "bad"} {
        dest := "good"
        if name == "bad" {
            dest = "secret-host"
        }
        if code, out, stderr := run(t, o, "add", name, "--ssh", dest, "--json"); code != 0 {
            t.Fatal(out, stderr)
        }
    }
    report := filepath.Join(t.TempDir(), "diagnostic.json")
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "check", "--targets", "ok,bad", "--trace", "--report", report, "--lang", lang, "--json")
        if code != 1 || stderr != "" || strings.Contains(out, `"message":""`) || !strings.Contains(out, `"completed":1`) || !strings.Contains(out, `"failure_kind":"name_resolution"`) {
            t.Fatal(code, out, stderr)
        }
        data, err := os.ReadFile(report)
        if err != nil || !json.Valid(data) {
            t.Fatal(string(data), err)
        }
        for _, secret := range []string{"secret", "path-secret", "remote_dir", "destination", "local_tools", "bad", "good"} {
            if strings.Contains(string(data), secret) {
                t.Fatal("diagnostic leaked", secret, string(data))
            }
        }
        code, out, stderr = run(t, o, "check", "--targets", "ok,bad", "--lang", lang)
        if code != 1 || stderr == "" || out == "" {
            t.Fatal(code, out, stderr)
        }
    }
    unknown := []byte(`{"format_version":99}`)
    _ = os.WriteFile(report, unknown, 0600)
    code, out, stderr := run(t, o, "check", "--targets", "ok", "--report", report, "--json")
    if code != 2 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
    data, _ := os.ReadFile(report)
    if string(data) != string(unknown) {
        t.Fatal("unknown format overwritten")
    }
    for _, args := range [][]string{{"check", "--targets", "ok,ok"}, {"check", "--targets", "ok", "--target", "bad"}, {"check", "--targets="}} {
        code, out, stderr = run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" {
            t.Fatal(code, out, stderr)
        }
    }
}
