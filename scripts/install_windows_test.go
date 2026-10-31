package main

import (
    "archive/zip"
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync/atomic"
    "testing"
    "time"

    "golang.org/x/sys/windows"
)

func TestWindowsInstaller(t *testing.T) {
    powershell, err := exec.LookPath("powershell.exe")
    if err != nil {
        t.Fatal("Windows PowerShell is required for installer acceptance:", err)
    }
    script, err := os.ReadFile(filepath.Join("..", "install.ps1"))
    if err != nil {
        t.Fatal(err)
    }
    for _, tc := range []struct {
        name, version, architecture, feed                        string
        badChecksum, missingChecksum, locked, repeat, defaultDir bool
        errorText                                                string
    }{
        {name: "latest-rc-overwrites-without-backups", repeat: true, defaultDir: true},
        {name: "explicit-version", version: "v1.0.0"},
        {name: "bad-checksum-preserves-executable", badChecksum: true, errorText: "Checksum verification failed"},
        {name: "missing-checksum-preserves-executable", missingChecksum: true, errorText: "Missing or duplicate checksum"},
        {name: "locked-executable-is-not-deleted", locked: true, errorText: "close Loaf or Bridge processes"},
        {name: "native-arm64-is-rejected", architecture: "ARM64", errorText: "Windows x64 only"},
        {name: "empty-release-feed", feed: `<feed xmlns="http://www.w3.org/2005/Atom"><id>tag:github.com,2008:https://github.com/elebirds/loaf/releases</id></feed>`, errorText: "No published Loaf release was found"},
        {name: "invalid-feed-tag", feed: `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>tag:github.com,2008:Repository/1426692274/latest</id></entry></feed>`, errorText: "Invalid Loaf release version"},
    } {
        t.Run(tc.name, func(t *testing.T) {
            root := filepath.Join(t.TempDir(), "Windows 中文 installer")
            local := filepath.Join(root, "local app data")
            installDir := filepath.Join(root, "selected install")
            if tc.defaultDir {
                installDir = filepath.Join(local, "Loaf", "bin")
            }
            if err := os.MkdirAll(installDir, 0700); err != nil {
                t.Fatal(err)
            }
            write := func(name, text string) {
                t.Helper()
                if err := os.WriteFile(name, []byte(text), 0600); err != nil {
                    t.Fatal(err)
                }
            }
            installer := filepath.Join(root, "install.ps1")
            write(installer, string(script))
            executable := filepath.Join(installDir, "loaf.exe")
            write(executable, "old executable")
            write(filepath.Join(installDir, "unrelated.txt"), "keep me")
            version := tc.version
            if version == "" {
                version = "v1.0.0-rc.9"
            }
            var archive bytes.Buffer
            zw := zip.NewWriter(&archive)
            for name, body := range map[string]string{
                "loaf.exe":                  "new executable " + version,
                "README.md":                 "[Installation](manual/en/installation.md)",
                "manual/en/installation.md": "Windows installation",
                "BUILD.json":                `{"version":"` + version + `"}`,
                "skills/loaf/SKILL.md":      "Loaf skill",
            } {
                f, err := zw.Create(name)
                if err != nil {
                    t.Fatal(err)
                }
                if _, err := f.Write([]byte(body)); err != nil {
                    t.Fatal(err)
                }
            }
            if err := zw.Close(); err != nil {
                t.Fatal(err)
            }
            sum := sha256.Sum256(archive.Bytes())
            checksum := hex.EncodeToString(sum[:])
            if tc.badChecksum {
                checksum = strings.Repeat("0", 64)
            }
            archiveName := "loaf_" + version + "_windows_amd64.zip"
            checksumName := archiveName
            if tc.missingChecksum {
                checksumName = "some-other-archive.zip"
            }
            var releaseCalls atomic.Int32
            server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                switch r.URL.Path {
                case "/releases":
                    releaseCalls.Add(1)
                    if r.URL.RawQuery != "" {
                        t.Error("release discovery must use the Atom feed without API query parameters")
                    }
                    w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
                    feed := tc.feed
                    if feed == "" {
                        feed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:github.com,2008:https://github.com/elebirds/loaf/releases</id>
  <entry><id>tag:github.com,2008:Repository/1426692274/v1.0.0-rc.9</id><title>Release candidate</title></entry>
  <entry><id>tag:github.com,2008:Repository/1426692274/v0.9.0</id><title>Older stable release</title></entry>
</feed>`
                    }
                    fmt.Fprint(w, feed)
                case "/downloads/" + version + "/" + archiveName:
                    _, _ = w.Write(archive.Bytes())
                case "/downloads/" + version + "/loaf_" + version + "_checksums.txt":
                    fmt.Fprintf(w, "%s  %s\n", checksum, checksumName)
                default:
                    t.Errorf("unexpected installer request: %s", r.URL.String())
                    http.NotFound(w, r)
                }
            }))
            defer server.Close()
            lockedHandle := windows.InvalidHandle
            unlock := func() {
                if lockedHandle != windows.InvalidHandle {
                    _ = windows.CloseHandle(lockedHandle)
                    lockedHandle = windows.InvalidHandle
                }
            }
            defer unlock()
            if tc.locked {
                name, err := windows.UTF16PtrFromString(executable)
                if err != nil {
                    t.Fatal(err)
                }
                lockedHandle, err = windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
                if err != nil {
                    t.Fatal(err)
                }
            }
            env := []string{}
            for _, value := range os.Environ() {
                name, _, _ := strings.Cut(value, "=")
                if !strings.HasPrefix(strings.ToUpper(name), "LOAF_") && !strings.EqualFold(name, "LOCALAPPDATA") && !strings.EqualFold(name, "PROCESSOR_ARCHITECTURE") && !strings.EqualFold(name, "PROCESSOR_ARCHITEW6432") {
                    env = append(env, value)
                }
            }
            architecture := "AMD64"
            if tc.architecture != "" {
                architecture = tc.architecture
            }
            env = append(env, "LOCALAPPDATA="+local, "PROCESSOR_ARCHITECTURE="+architecture,
                "LOAF_RELEASES_URL="+server.URL+"/releases", "LOAF_DOWNLOAD_URL="+server.URL+"/downloads", "LOAF_NO_MODIFY_PATH=1")
            if !tc.defaultDir {
                env = append(env, "LOAF_INSTALL_DIR="+installDir)
            }
            if tc.version != "" {
                env = append(env, "LOAF_VERSION="+tc.version)
            }
            run := func() {
                t.Helper()
                ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
                defer cancel()
                cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", installer)
                cmd.Env = env
                out, err := cmd.CombinedOutput()
                output := strings.Join(strings.Fields(string(out)), " ")
                if (err != nil) != (tc.errorText != "") || ctx.Err() != nil || tc.errorText != "" && !strings.Contains(output, tc.errorText) {
                    t.Fatalf("installer: %v\n%s", err, out)
                }
            }
            run()
            if tc.repeat {
                run()
            }
            unlock()
            data, err := os.ReadFile(executable)
            expected := "new executable " + version
            if tc.errorText != "" {
                expected = "old executable"
            }
            if err != nil || string(data) != expected {
                t.Fatalf("executable changed incorrectly: %q, %v", data, err)
            }
            if data, err := os.ReadFile(filepath.Join(installDir, "unrelated.txt")); err != nil || string(data) != "keep me" {
                t.Fatal("unrelated file changed", string(data), err)
            }
            if tc.errorText == "" {
                for _, name := range []string{"README.md", "manual/en/installation.md", "skills/loaf/SKILL.md", "BUILD.json"} {
                    if _, err := os.Stat(filepath.Join(installDir, filepath.FromSlash(name))); err != nil {
                        t.Fatal("missing installed package content", name, err)
                    }
                }
            }
            if err := filepath.WalkDir(installDir, func(path string, entry os.DirEntry, err error) error {
                if err != nil {
                    return err
                }
                if strings.Contains(entry.Name(), ".bak") || strings.Contains(entry.Name(), "rc.9") || strings.Contains(entry.Name(), "v1.0.0") {
                    t.Errorf("unexpected retained version or backup: %s", path)
                }
                return nil
            }); err != nil {
                t.Fatal(err)
            }
            if tc.version != "" && releaseCalls.Load() != 0 || tc.architecture != "" && releaseCalls.Load() != 0 {
                t.Fatal("explicit version/unsupported architecture should not query latest", releaseCalls.Load())
            }
        })
    }
}

func TestWindowsInstallerPathPreservesAndDeduplicates(t *testing.T) {
    powershell, err := exec.LookPath("powershell.exe")
    if err != nil {
        t.Fatal(err)
    }
    installer, err := filepath.Abs(filepath.Join("..", "install.ps1"))
    if err != nil {
        t.Fatal(err)
    }
    // Run the production PATH function while replacing only its two registry
    // boundaries. No real user/system PATH or registry entry is changed.
    wrapper := `
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
$tokens = $null; $errors = $null
$source = Get-Content -LiteralPath $env:LOAF_INSTALLER_SCRIPT -Raw -Encoding UTF8
$ast = [System.Management.Automation.Language.Parser]::ParseInput($source, [ref] $tokens, [ref] $errors)
if ($errors.Count) { throw ($errors | Out-String) }
$function = $ast.Find({ param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Add-LoafPath' }, $true)
Invoke-Expression $function.Extent.Text
$script:userPath = '%USERPROFILE%\custom;C:\existing tools'
$script:writes = 0
function Get-LoafUserPath { return $script:userPath }
function Set-LoafUserPath { param([string] $Value) $script:userPath = $Value; $script:writes++ }
$env:Path = 'C:\system;C:\system tools'
$directory = 'C:\Users\Installer User\Loaf\bin'
Add-LoafPath $directory
Add-LoafPath $directory
if ($script:writes -ne 1) { throw 'PATH persisted more than once' }
if ($script:userPath -cne ($directory + ';%USERPROFILE%\custom;C:\existing tools')) { throw 'Existing raw user PATH was changed' }
if ($env:Path -cne ($directory + ';C:\system;C:\system tools')) { throw 'Existing process PATH was changed' }
$env:LOAF_PATH_ROOT = 'C:\Users\Installer User\Loaf'
$script:userPath = '%LOAF_PATH_ROOT%\bin;C:\untouched'
$script:writes = 0
$env:Path = 'c:\users\installer user\loaf\bin\;C:\unchanged'
Add-LoafPath $directory
if ($script:writes -ne 0 -or $script:userPath -cne '%LOAF_PATH_ROOT%\bin;C:\untouched') { throw 'Expanded user PATH was duplicated or rewritten' }
if ($env:Path -cne 'c:\users\installer user\loaf\bin\;C:\unchanged') { throw 'Equivalent process PATH was duplicated' }
@{ok=$true} | ConvertTo-Json -Compress
`
    root := t.TempDir()
    script := filepath.Join(root, "path-test.ps1")
    if err := os.WriteFile(script, []byte(wrapper), 0600); err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
    cmd.Env = append(os.Environ(), "LOAF_INSTALLER_SCRIPT="+installer)
    out, err := cmd.CombinedOutput()
    var result struct {
        OK bool `json:"ok"`
    }
    if err != nil || json.Unmarshal(bytes.TrimSpace(out), &result) != nil || !result.OK {
        t.Fatalf("PATH handling: %v\n%s", err, out)
    }
}
