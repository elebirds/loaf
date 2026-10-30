package integration

import (
    "bytes"
    "context"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/i18n"
)

func TestUXEPublicHumanLogsPreserveBytesAndFrozenBinding(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    fixture := newFixture(t)
    project := stopProject(t, fixture)
    expected := "first 中文\n\x00\x1b[31mfixture-body\x1b[0m\n"
    accepted := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", `printf 'first 中文\n\000\033[31mfixture-body\033[0m\n'; exit 7`)
    if accepted.Status != "failed" || accepted.ExitCode == nil || *accepted.ExitCode != 7 {
        t.Fatal(accepted)
    }
    // A different current alias must not redirect the saved log binding.
    for _, args := range [][]string{{"machine", "add", "other", "--ssh", "ssh -p 1 researcher@127.0.0.1", "--json"}, {"machine", "use", "other", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    for _, language := range []string{"en", "zh-Hans"} {
        ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
        checkpoint := filepath.Join(t.TempDir(), "cursor.json")
        cmd := stopCLICommand(ctx, project, "logs", accepted.ID, "-f", "--resume-file", checkpoint, "--lang", language)
        var stderr bytes.Buffer
        cmd.Stderr = &stderr
        out, err := cmd.Output()
        cancel()
        l, _ := i18n.New(language, nil)
        if err != nil || string(out) != expected || !strings.Contains(stderr.String(), accepted.ID) || strings.Contains(stderr.String(), "fixture-body") || strings.Count(stderr.String(), l.Text("ux_e_follow_done")) != 1 || !strings.Contains(stderr.String(), l.Text("run_exit", map[string]any{"Value": 7})) {
            t.Fatal(err, string(out), stderr.String())
        }
        if _, err := os.Stat(checkpoint); !errors.Is(err, os.ErrNotExist) {
            t.Fatal("pipe auto-acknowledged logs", err)
        }
    }
}
