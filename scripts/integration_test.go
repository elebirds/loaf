package main

import (
    "context"
    "os"
    "os/exec"
    "path/filepath"
    "reflect"
    "runtime"
    "strings"
    "testing"
    "time"
)

func TestV1CompatibilityIntegrationGate(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("Unix Docker CI entry point; native Windows uses its separate packaging path")
    }
    script, err := os.ReadFile("integration.sh")
    if err != nil {
        t.Fatal(err)
    }
    bash, err := exec.LookPath("bash")
    if err != nil {
        t.Fatal(err)
    }
    for _, tc := range []struct {
        name, ref string
        tag       bool
        explicit  bool
        fail      bool
        builds    []string
        download  bool
    }{
        {name: "first-release-without-tag", builds: []string{"v1.0.0", "v1.1.0"}},
        {name: "initial-release-tag", tag: true, ref: "refs/tags/v1.0.0", builds: []string{"v1.0.0", "v1.1.0"}},
        {name: "released-baseline", tag: true, ref: "refs/heads/main", builds: []string{"v1.1.0"}, download: true},
        {name: "failed-baseline-download", tag: true, fail: true, download: true},
        {name: "explicit-old-binary", tag: true, explicit: true, builds: []string{"v1.1.0"}},
    } {
        t.Run(tc.name, func(t *testing.T) {
            root := filepath.Join(t.TempDir(), "fresh checkout")
            bin := filepath.Join(root, "mock-bin")
            for _, dir := range []string{bin, filepath.Join(root, "scripts")} {
                if err := os.MkdirAll(dir, 0700); err != nil {
                    t.Fatal(err)
                }
            }
            write := func(path, source string) {
                t.Helper()
                if err := os.WriteFile(path, []byte(source), 0700); err != nil {
                    t.Fatal(err)
                }
            }
            write(filepath.Join(root, "scripts", "integration.sh"), string(script))
            // Only native file operations run. Every external tool and the
            // complete product build are replaced inside this private checkout.
            mocks := map[string]string{
                "docker": `case "$*" in
    info|build\ *) ;;
    'image inspect loaf-sshd-test:local --format {{.Architecture}}') printf 'amd64\n' ;;
    *) exit 90 ;;
esac`,
                "git": `test "$*" = 'tag --list v1.0.0'
printf 'git\n' >>"$CALLS"
if test "$HAS_TAG" = 1; then printf 'v1.0.0\n'; fi`,
                "curl": `printf 'curl:%s\n' "$*" >>"$CALLS"
test "$1" = --fail
if test "$FAIL_DOWNLOAD" = 1; then exit 22; fi
while test "$1" != --output; do shift; done
printf 'v1.0.0\n' >"$2"`,
                "tar": `printf 'tar:%s\n' "$*" >>"$CALLS"
test "$1" = -xzf
test "$3" = -C
version=$(cat "$2")
printf '#!/bin/sh\nprintf "%%s\n" "%s"\n' "$version" >"$4/loaf"
chmod 0700 "$4/loaf"`,
                "go": `case "$*" in
    'env GOOS') printf 'linux\n' ;;
    'env GOARCH') printf 'amd64\n' ;;
    'test -c '*) ;;
    'test '*)
        test "$LOAF_INTEGRATION" = 1
        old=$("$LOAF_COMPAT_OLD_BIN")
        new=$("$LOAF_BIN")
        printf 'integration:%s:%s:%s\nold-bin:%s\nargs:%s\n' "$old" "$new" "$VERSION" "$LOAF_COMPAT_OLD_BIN" "$*" >>"$CALLS"
        ;;
    *) exit 91 ;;
esac`,
            }
            for name, body := range mocks {
                write(filepath.Join(bin, name), "#!/bin/sh\nset -eu\n"+body+"\n")
            }
            write(filepath.Join(root, "scripts", "build.sh"), `#!/bin/sh
set -eu
printf 'build:%s\n' "$VERSION" >>"$CALLS"
test "$LOAF_BUILD_TARGETS" = linux/amd64
mkdir -p dist
printf '%s\n' "$VERSION" >dist/version
printf '%s\n' "$VERSION" >"dist/loaf_${VERSION}_linux_amd64.tar.gz"
printf '#!/bin/sh\nprintf "%%s\n" "%s"\n' "$VERSION" >dist/loaf
chmod 0700 dist/loaf
`)
            calls := filepath.Join(root, "calls")
            // No pre-existing dist directory: CI must create its temporary
            // baseline directory before the first build can create dist.
            env := []string{}
            for _, value := range os.Environ() {
                key, _, _ := strings.Cut(value, "=")
                if key != "PATH" && key != "VERSION" && key != "GITHUB_REF" && !strings.HasPrefix(key, "LOAF_") {
                    env = append(env, value)
                }
            }
            env = append(env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "LOAF_V1_COMPAT=1", "CALLS="+calls, "HAS_TAG=0", "FAIL_DOWNLOAD=0", "GITHUB_REF="+tc.ref)
            if tc.tag {
                env = append(env, "HAS_TAG=1")
            }
            if tc.fail {
                env = append(env, "FAIL_DOWNLOAD=1")
            }
            explicit := filepath.Join(root, "provided loaf")
            if tc.explicit {
                write(explicit, "#!/bin/sh\nprintf 'v1.0.0\\n'\n")
                env = append(env, "LOAF_COMPAT_OLD_BIN="+explicit)
            }
            ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
            defer cancel()
            command := exec.CommandContext(ctx, bash, "scripts/integration.sh", "-run", "TestPackagedV1Compatibility")
            command.Dir, command.Env = root, env
            out, runErr := command.CombinedOutput()
            data, readErr := os.ReadFile(calls)
            if ctx.Err() != nil || readErr != nil || (runErr != nil) != tc.fail {
                t.Fatalf("integration exit: %v; read: %v\n%s\n%s", runErr, readErr, out, data)
            }
            trace := string(data)
            var builds []string
            for _, line := range strings.Split(trace, "\n") {
                if version, ok := strings.CutPrefix(line, "build:"); ok {
                    builds = append(builds, version)
                }
            }
            if !reflect.DeepEqual(builds, tc.builds) || strings.Contains(trace, "curl:") != tc.download {
                t.Fatal("wrong baseline source or rebuild", builds, trace)
            }
            if tc.download && !strings.Contains(trace, "https://github.com/elebirds/loaf/releases/download/v1.0.0/loaf_v1.0.0_linux_amd64.tar.gz") {
                t.Fatal("did not request the actual first-release artifact", trace)
            }
            if tc.fail {
                exited, ok := runErr.(*exec.ExitError)
                if !ok || exited.ExitCode() != 22 || strings.Contains(trace, "tar:") || strings.Contains(trace, "integration:") {
                    t.Fatal("failed baseline download silently continued", trace)
                }
                return
            }
            if !strings.Contains(trace, "integration:v1.0.0:v1.1.0:v1.1.0\n") || !strings.Contains(trace, "github.com/elebirds/loaf/internal/wire.Version=v1.1.0") || !strings.Contains(trace, "-run TestPackagedV1Compatibility ./tests/integration") {
                t.Fatal("compatibility suite did not receive two distinct formal candidates", trace)
            }
            if tc.explicit && (!strings.Contains(trace, "old-bin:"+explicit+"\n") || strings.Contains(trace, "git\n")) {
                t.Fatal("explicit baseline was replaced or unnecessarily rediscovered", trace)
            }
        })
    }
}
