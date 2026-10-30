package integration

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/ssh"
)

func TestWindowsOutputNamespaceAndCrossVolume(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    f := newFixture(t)
    project := filepath.Join(t.TempDir(), "项目 space")
    if err := os.Mkdir(project, 0700); err != nil {
        t.Fatal(err)
    }
    for _, args := range [][]string{{"machine", "add", "lab", "--ssh", "ssh -F " + ssh.Quote(f.config) + " fixture", "--json"}, {"machine", "use", "lab", "--json"}} {
        if code, out := cli(t, project, args...); code != 0 {
            t.Fatal(code, string(out))
        }
    }
    started := runCLI(t, project, "run", "--wait", "5s", "--json", "--", "/bin/sh", "-c", `mkdir -p outputs/good outputs/case outputs/reserved outputs/invalid; printf good >'outputs/good/中文 space.txt'; printf A >outputs/case/A; printf a >outputs/case/a; printf reserved >outputs/reserved/NUL.txt; printf invalid >'outputs/invalid/name?'; printf stop >'outputs/trailing.'`)
    before := f.remoteRecord(started.RemoteDir, started.ID)
    for _, name := range []string{"outputs/case", "outputs/reserved", "outputs/invalid", "outputs/trailing."} {
        destination := filepath.Join(t.TempDir(), "uncreated")
        code, out := cli(t, project, "pull", started.ID, "--path", name, "--to", destination, "--json")
        if code != 1 || !bytes.Contains(out, []byte(`"code":"UNSAFE_PATH"`)) {
            t.Fatal(name, code, string(out))
        }
        if _, err := os.Stat(destination); !os.IsNotExist(err) {
            t.Fatal("rejected names created destination", err)
        }
    }
    collision := t.TempDir()
    sourceFile(t, collision, "Outputs/good/中文 space.txt", "keep")
    code, out := cli(t, project, "pull", started.ID, "--path", "outputs/good", "--to", collision, "--json")
    if code != 1 || !bytes.Contains(out, []byte(`"code":"UNSAFE_PATH"`)) {
        t.Fatal(code, string(out))
    }
    if data, err := os.ReadFile(filepath.Join(collision, "Outputs/good/中文 space.txt")); err != nil || string(data) != "keep" {
        t.Fatal("collision overwrote local file", err)
    }
    outside, destination := t.TempDir(), t.TempDir()
    junction := filepath.Join(destination, "outputs")
    command(t, "cmd.exe", "/c", "mklink", "/J", junction, outside)
    t.Cleanup(func() { _ = os.Remove(junction) })
    code, out = cli(t, project, "pull", started.ID, "--path", "outputs/good", "--to", destination, "--json")
    if code != 1 || !bytes.Contains(out, []byte(`"code":"UNSAFE_PATH"`)) {
        t.Fatal("junction accepted", code, string(out))
    }
    if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
        t.Fatal("wrote through junction", entries, err)
    }
    if after := f.remoteRecord(started.RemoteDir, started.ID); after != before {
        t.Fatal("destination constraints changed the run record")
    }
    t.Run("cross-volume", func(t *testing.T) {
        base := os.Getenv("LOAF_WINDOWS_TEST_VOLUME")
        if base == "" {
            t.Skip("set LOAF_WINDOWS_TEST_VOLUME to an independent local NTFS volume")
        }
        if !filepath.IsAbs(base) || strings.EqualFold(filepath.VolumeName(base), filepath.VolumeName(project)) {
            t.Fatal("an independent absolute volume is required")
        }
        destination, err := os.MkdirTemp(base, "loaf-windows-test-")
        if err != nil {
            t.Fatal(err)
        }
        relative, err := filepath.Rel(base, destination)
        if err != nil || !filepath.IsLocal(relative) || !strings.HasPrefix(relative, "loaf-windows-test-") {
            t.Fatal("unexpected temporary destination")
        }
        t.Cleanup(func() {
            if err := os.RemoveAll(destination); err != nil {
                t.Error(err)
            }
        })
        destination = filepath.Join(destination, "跨盘 space")
        result := pullCLI(t, project, 0, started.ID, "--path", "outputs/good", "--to", destination, "--json")
        if data, err := os.ReadFile(filepath.Join(result.Destination, "outputs/good/中文 space.txt")); err != nil || string(data) != "good" {
            t.Fatal("cross-volume download", err)
        }
        // The original run remains accessible under alternate drive/path case.
        if code, out := cli(t, strings.ToUpper(project), "status", started.ID, "--local", "--json"); code != 0 {
            t.Fatal("project identity changed with path spelling", code, string(out))
        }
    })
}
