package cli

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestSyncDryRunDoesNotConnectOrCreateState(t *testing.T) {
    o := fixture(t)
    if err := os.WriteFile(filepath.Join(o.CWD, "train.py"), []byte("print(1)\n"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(o.CWD, ".env"), []byte("secret"), 0600); err != nil {
        t.Fatal(err)
    }
    // No machine, SSH client, packaged runner, or saved project is needed.
    code, out, stderr := run(t, o, "sync", "--dry-run", "--json")
    if code != 0 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
    var response struct {
        OK   bool       `json:"ok"`
        Data syncResult `json:"data"`
    }
    if json.Unmarshal([]byte(out), &response) != nil || !response.OK || !response.Data.DryRun || response.Data.SelectedFiles != 1 || response.Data.ChangedFiles != nil || len(response.Data.Files) != 1 || response.Data.Files[0].Path != "train.py" {
        t.Fatal(out)
    }
    if _, err := os.Stat(filepath.Dir(o.Store.IndexFile)); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("preview wrote state", err)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "sync", "--dry-run")
        if code != 0 || stderr != "" || !strings.Contains(out, "train.py") || !strings.Contains(out, ".env") || strings.Contains(out, "<no value>") {
            t.Fatal(code, out, stderr)
        }
        if lang == "zh-Hans" && !strings.Contains(out, "排除的路径及规则") {
            t.Fatal(out)
        }
    }
}

func TestCanceledPreviewReportsCancellationRatherThanSourceFailure(t *testing.T) {
    o := fixture(t)
    for _, lang := range []string{"en", "zh-Hans"} {
        ctx, cancel := context.WithCancel(context.Background())
        cancel()
        var out, diagnostic strings.Builder
        o.Out, o.Err = &out, &diagnostic
        code := Execute(ctx, []string{"--lang", lang, "sync", "--dry-run", "--json"}, o)
        if code != 1 || diagnostic.Len() != 0 || !strings.Contains(out.String(), `"code":"SYNC_CANCELED"`) {
            t.Fatal(code, out.String(), diagnostic.String())
        }
    }
}

func TestSyncPolicyPrecedenceAndAppendOnlyExclusions(t *testing.T) {
    o := fixture(t)
    if err := os.MkdirAll(filepath.Dir(o.Store.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    global := "format_version = 1\n[sync]\nmode = 'update'\nrespect_gitignore = false\nexclude = ['global.py']\n"
    if err := os.WriteFile(o.Store.ConfigFile, []byte(global), 0600); err != nil {
        t.Fatal(err)
    }
    project := "[sync]\nmode = 'mirror'\nexclude = ['project.py']\n"
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(project), 0600); err != nil {
        t.Fatal(err)
    }
    for _, name := range []string{"global.py", "project.py", "command.py", "ignored.py", ".env"} {
        if err := os.WriteFile(filepath.Join(o.CWD, name), []byte("data"), 0600); err != nil {
            t.Fatal(err)
        }
    }
    if err := os.WriteFile(filepath.Join(o.CWD, ".gitignore"), []byte("ignored.py\n"), 0600); err != nil {
        t.Fatal(err)
    }
    code, out, stderr := run(t, o, "sync", "--dry-run", "--exclude", "command.py", "--json")
    var response struct {
        Data syncResult `json:"data"`
    }
    if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &response) != nil {
        t.Fatal(code, out, stderr)
    }
    p := response.Data.Policy
    if p.Mode != "mirror" || p.RespectGitignore || strings.Join(p.Exclude, ",") != "global.py,project.py,command.py" || len(response.Data.Files) != 3 {
        t.Fatal(out)
    }
    code, out, stderr = run(t, o, "sync", "--dry-run", "--mode", "update", "--respect-gitignore", "--json")
    if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &response) != nil || response.Data.Policy.Mode != "update" || !response.Data.Policy.RespectGitignore {
        t.Fatal(code, out, stderr)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "sync", "--dry-run", "--mode", "update")
        if code != 0 || stderr != "" || !strings.Contains(out, "update") || strings.Contains(out, "<no value>") {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "--lang", lang, "sync", "--dry-run", "--mode", "typo", "--json")
        if code != 2 || stderr != "" || !strings.Contains(out, `"code":"BAD_CONFIG"`) {
            t.Fatal(code, out, stderr)
        }
    }
}

func TestSourcePreviewRequiresExplicitDryRun(t *testing.T) {
    o := fixture(t)
    for _, args := range [][]string{{"sync", "--json"}, {"sync", "--dry-run=false", "--json"}} {
        code, out, stderr := run(t, o, args...)
        if code != 2 || stderr != "" || !strings.Contains(out, "BAD_CONFIG") {
            t.Fatal(code, out, stderr)
        }
    }
}
