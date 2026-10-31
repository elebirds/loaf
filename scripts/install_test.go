//go:build !windows

package main

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "context"
    "crypto/sha256"
    "fmt"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"
)

const installerVersion = "v1.2.3-rc.1"

type unixInstallerFixture struct {
    t             *testing.T
    home, mockBin string
    temp, install string
    script, url   string
    badChecksum   bool
    requests      []string
    mu            sync.Mutex
}

func newUnixInstallerFixture(t *testing.T, system, arch string, badChecksum bool) *unixInstallerFixture {
    t.Helper()
    root := t.TempDir()
    script, err := filepath.Abs("../install.sh")
    if err != nil {
        t.Fatal(err)
    }
    f := &unixInstallerFixture{t: t, home: filepath.Join(root, "home ' with spaces"), mockBin: filepath.Join(root, "tools"), temp: filepath.Join(root, "temporary"), script: script, badChecksum: badChecksum}
    f.install = filepath.Join(f.home, ".local/share/loaf/cli")
    for _, dir := range []string{f.home, f.mockBin, f.temp} {
        if err := os.MkdirAll(dir, 0700); err != nil {
            t.Fatal(err)
        }
    }
    f.write(filepath.Join(f.mockBin, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo "+system+" ;; -m) echo "+arch+" ;; *) exit 1 ;; esac\n", 0700)
    var archive bytes.Buffer
    compressed := gzip.NewWriter(&archive)
    writer := tar.NewWriter(compressed)
    files := map[string]string{
        "loaf":                      "#!/bin/sh\nprintf '%s\\n' '" + installerVersion + "'\n",
        "README.md":                 "fixture README\n",
        "manual/en/installation.md": "fixture manual\n",
        "skills/loaf/SKILL.md":      "fixture Skill\n",
        "LICENSE":                   "fixture license\n",
    }
    for name, content := range files {
        header := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
        if name == "loaf" {
            header.Mode = 0755
        }
        if err := writer.WriteHeader(header); err != nil {
            t.Fatal(err)
        }
        if _, err := writer.Write([]byte(content)); err != nil {
            t.Fatal(err)
        }
    }
    if err := writer.Close(); err != nil {
        t.Fatal(err)
    }
    if err := compressed.Close(); err != nil {
        t.Fatal(err)
    }
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        f.mu.Lock()
        f.requests = append(f.requests, r.URL.RequestURI())
        f.mu.Unlock()
        switch {
        case r.URL.Path == "/releases" && r.URL.Query().Get("per_page") == "1":
            // Compact JSON, with an older formal release after the default RC.
            fmt.Fprintf(w, `[{"tag_name":%q,"prerelease":true},{"tag_name":"v1.1.0","prerelease":false}]`, installerVersion)
        case strings.HasPrefix(r.URL.Path, "/download/"+installerVersion+"/") && strings.HasSuffix(r.URL.Path, ".tar.gz"):
            w.Write(archive.Bytes())
        case r.URL.Path == "/download/"+installerVersion+"/loaf_"+installerVersion+"_checksums.txt":
            sum := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
            if f.badChecksum {
                sum = strings.Repeat("0", 64)
            }
            for _, target := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
                fmt.Fprintf(w, "%s  loaf_%s_%s.tar.gz\n", sum, installerVersion, target)
            }
        default:
            http.NotFound(w, r)
        }
    }))
    t.Cleanup(server.Close)
    f.url = server.URL
    return f
}

func (f *unixInstallerFixture) write(path, value string, mode os.FileMode) {
    f.t.Helper()
    if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
        f.t.Fatal(err)
    }
    if err := os.WriteFile(path, []byte(value), mode); err != nil {
        f.t.Fatal(err)
    }
}

func (f *unixInstallerFixture) read(path string) string {
    f.t.Helper()
    value, err := os.ReadFile(path)
    if err != nil {
        f.t.Fatal(err)
    }
    return string(value)
}

func (f *unixInstallerFixture) env(shell string, extra ...string) []string {
    env := []string{}
    for _, value := range os.Environ() {
        key, _, _ := strings.Cut(value, "=")
        if key != "HOME" && key != "PATH" && key != "SHELL" && key != "TMPDIR" && key != "ZDOTDIR" && !strings.HasPrefix(key, "LOAF_") && !strings.HasPrefix(key, "XDG_") {
            env = append(env, value)
        }
    }
    return append(env, append([]string{"HOME=" + f.home, "PATH=" + f.mockBin + ":" + os.Getenv("PATH"), "TMPDIR=" + f.temp, "SHELL=" + shell, "LOAF_RELEASES_URL=" + f.url + "/releases", "LOAF_DOWNLOAD_URL=" + f.url + "/download"}, extra...)...)
}

func (f *unixInstallerFixture) run(shell string, extra ...string) (string, error) {
    f.t.Helper()
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    command := exec.CommandContext(ctx, "sh", f.script)
    command.Env = f.env(shell, extra...)
    out, err := command.CombinedOutput()
    return string(out), err
}

func TestUnixInstallerLatestRCOverwriteAndPATH(t *testing.T) {
    f := newUnixInstallerFixture(t, "Linux", "x86_64", false)
    binary := filepath.Join(f.install, "loaf")
    bashRC, bashProfile := filepath.Join(f.home, ".bashrc"), filepath.Join(f.home, ".bash_profile")
    f.write(binary, "old binary", 0700)
    f.write(bashRC, "export KEEP_ME=unchanged", 0600)
    f.write(bashProfile, "# existing login profile\n", 0600)
    f.write(filepath.Join(f.install, "user-note.txt"), "unrelated file", 0600)
    state := filepath.Join(f.home, ".local/state/loaf/state-v4/state.db")
    f.write(state, "existing state", 0600)
    for attempt := 0; attempt < 2; attempt++ {
        out, err := f.run("/bin/bash")
        if err != nil || !strings.Contains(out, installerVersion) {
            t.Fatalf("install: %v\n%s", err, out)
        }
        if !strings.Contains(f.read(binary), installerVersion) || f.read(filepath.Join(f.install, "manual/en/installation.md")) != "fixture manual\n" {
            t.Fatal("executable or complete package missing")
        }
        if strings.Count(f.read(bashRC), "# Loaf") != 1 || strings.Count(f.read(bashProfile), "# Loaf") != 1 || !strings.HasPrefix(f.read(bashRC), "export KEEP_ME=unchanged\n") {
            t.Fatal("startup settings replaced or duplicated")
        }
    }
    if f.read(state) != "existing state" || f.read(filepath.Join(f.install, "user-note.txt")) != "unrelated file" {
        t.Fatal("unrelated state or files changed")
    }
    entries, err := os.ReadDir(f.install)
    if err != nil || len(entries) != 6 { // five package roots plus the unrelated note
        t.Fatal("unexpected installation inventory or retained backup", entries, err)
    }
    command := exec.Command("bash", "--noprofile", "--norc", "-c", `. "$HOME/.bashrc"; . "$HOME/.bashrc"; . "$HOME/.bash_profile"; printf '%s\n' "$PATH"; loaf`)
    command.Env = f.env("/bin/bash")
    out, err := command.CombinedOutput()
    if err != nil || strings.Count(strings.SplitN(string(out), "\n", 2)[0], f.install) != 1 || !strings.HasSuffix(string(out), installerVersion+"\n") {
        t.Fatalf("new shell PATH: %v\n%s", err, out)
    }
    leftovers, err := os.ReadDir(f.temp)
    if err != nil || len(leftovers) != 0 {
        t.Fatal("temporary downloads retained", leftovers, err)
    }
}

func TestUnixInstallerChecksumFailurePreservesInstallation(t *testing.T) {
    f := newUnixInstallerFixture(t, "Darwin", "arm64", true)
    binary, profile := filepath.Join(f.install, "loaf"), filepath.Join(f.home, ".zshrc")
    f.write(binary, "old binary", 0700)
    f.write(profile, "# unchanged shell\n", 0600)
    out, err := f.run("/bin/zsh")
    if err == nil || !strings.Contains(out, "Checksum mismatch") || f.read(binary) != "old binary" || f.read(profile) != "# unchanged shell\n" {
        t.Fatalf("bad download mutated installation: %v\n%s", err, out)
    }
}

func TestUnixInstallerPlatformAndShells(t *testing.T) {
    for _, tc := range []struct {
        os, arch, target, shell, profile string
    }{
        {"Darwin", "arm64", "darwin_arm64", "/bin/zsh", "zsh settings/.zshrc"},
        {"Darwin", "x86_64", "darwin_amd64", "/bin/sh", ".profile"},
        {"Linux", "aarch64", "linux_arm64", "/usr/bin/fish", ".config/fish/conf.d/loaf.fish"},
    } {
        t.Run(tc.target, func(t *testing.T) {
            f := newUnixInstallerFixture(t, tc.os, tc.arch, false)
            custom := filepath.Join(f.home, "apps $literal `tick` \"quote\"")
            out, err := f.run(tc.shell, "LOAF_INSTALL_DIR="+custom, "ZDOTDIR="+filepath.Join(f.home, "zsh settings"))
            if err != nil {
                t.Fatalf("install: %v\n%s", err, out)
            }
            f.mu.Lock()
            requests := strings.Join(f.requests, "\n")
            f.mu.Unlock()
            if !strings.Contains(requests, "loaf_"+installerVersion+"_"+tc.target+".tar.gz") {
                t.Fatal("incorrect platform asset", requests)
            }
            profile := filepath.Join(f.home, filepath.FromSlash(tc.profile))
            value := f.read(profile)
            if !strings.Contains(value, `apps \$literal \`+"`"+`tick\`+"`"+` \"quote\"`) {
                t.Fatal("literal install path lost shell quoting", value)
            }
            executable := "sh"
            if strings.HasSuffix(tc.shell, "/zsh") {
                if _, err := exec.LookPath("zsh"); err != nil {
                    return // Native zsh is additionally exercised on macOS CI.
                }
                executable = "zsh"
            } else if strings.HasSuffix(tc.shell, "/fish") {
                // The written fish conf.d fragment preserves the existing PATH list.
                if !strings.Contains(value, "contains -- ") || !strings.Contains(value, "or set -gx PATH ") {
                    t.Fatal(value)
                }
                return
            }
            command := exec.Command(executable, "-c", `. "$1"; loaf`, "installer-shell-check", profile)
            command.Env = f.env(tc.shell)
            if out, err := command.CombinedOutput(); err != nil || string(out) != installerVersion+"\n" {
                t.Fatalf("activation: %v\n%s", err, out)
            }
        })
    }
}

func TestUnixInstallerExplicitVersionAndNoPATH(t *testing.T) {
    f := newUnixInstallerFixture(t, "Linux", "amd64", false)
    out, err := f.run("/bin/bash", "LOAF_VERSION="+installerVersion, "LOAF_NO_MODIFY_PATH=1")
    if err != nil {
        t.Fatalf("install: %v\n%s", err, out)
    }
    f.mu.Lock()
    requests := append([]string(nil), f.requests...)
    f.mu.Unlock()
    for _, request := range requests {
        if strings.HasPrefix(request, "/releases") {
            t.Fatal("explicit version queried release discovery", requests)
        }
    }
    if _, err := os.Stat(filepath.Join(f.home, ".bashrc")); !os.IsNotExist(err) {
        t.Fatal("PATH opt-out wrote shell settings", err)
    }
    // Opting out still prints an activation command for the selected shell.
    out, err = f.run("/usr/bin/fish", "LOAF_VERSION="+installerVersion, "LOAF_NO_MODIFY_PATH=1")
    if err != nil || !strings.Contains(out, "or set -gx PATH ") {
        t.Fatalf("fish opt-out activation: %v\n%s", err, out)
    }
}

func TestUnixInstallerUnsupportedArchitecture(t *testing.T) {
    f := newUnixInstallerFixture(t, "Linux", "riscv64", false)
    out, err := f.run("/bin/sh")
    if err == nil || !strings.Contains(out, "Supported architectures") {
        t.Fatalf("unsupported architecture: %v\n%s", err, out)
    }
    f.mu.Lock()
    defer f.mu.Unlock()
    if len(f.requests) != 0 {
        t.Fatal("unsupported platform attempted downloads", f.requests)
    }
}
