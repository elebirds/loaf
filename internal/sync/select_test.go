package sync

import (
    "context"
    "errors"
    "github.com/elebirds/loaf/internal/testutil"
    "github.com/elebirds/loaf/internal/wire"
    "os"
    "os/exec"
    "path/filepath"
    "reflect"
    "runtime"
    "slices"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/fault"
)

func put(t *testing.T, root, name, text string) {
    t.Helper()
    target := filepath.Join(root, filepath.FromSlash(name))
    if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(target, []byte(text), 0600); err != nil {
        t.Fatal(err)
    }
}

func git(t *testing.T, root string, args ...string) {
    t.Helper()
    cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
    if out, err := cmd.CombinedOutput(); err != nil {
        t.Fatalf("git %v: %v %s", args, err, out)
    }
}

func names(p Plan) []string {
    result := []string{}
    for _, f := range p.Files {
        result = append(result, f.Path)
    }
    return result
}

func isolation(t *testing.T) {
    t.Helper()
    t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
    t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}

func TestGitSelectionReadsWorkingFilesAndFiltersTrackedSecrets(t *testing.T) {
    isolation(t)
    root := t.TempDir()
    git(t, root, "init", "-q")
    for _, name := range []string{"tracked.py", "deleted.py", "tracked.log", ".env", "weights/model.pt"} {
        put(t, root, name, "old")
    }
    git(t, root, "add", ".")
    indexBefore, err := os.ReadFile(filepath.Join(root, ".git", "index"))
    if err != nil {
        t.Fatal(err)
    }
    put(t, root, ".gitignore", "*.log\n!keep.log\nnested/*\n!nested/keep.py\n")
    put(t, root, "tracked.py", "uncommitted contents")
    if err := os.Remove(filepath.Join(root, "deleted.py")); err != nil {
        t.Fatal(err)
    }
    for _, name := range []string{"new.py", "skip.log", "keep.log", "nested/skip.py", "nested/keep.py", ".venv/bin/python", "outputs/result.json", "data/train.csv", "docs/method.md"} {
        put(t, root, name, "content")
    }
    p, err := Select(context.Background(), root, SelectOptions{Exclude: []string{"/data/"}})
    if err != nil {
        t.Fatal(err)
    }
    want := []string{".gitignore", "docs/method.md", "keep.log", "nested/keep.py", "new.py", "tracked.log", "tracked.py"}
    if !reflect.DeepEqual(names(p), want) {
        t.Fatalf("got %v want %v", names(p), want)
    }
    for _, file := range p.Files {
        if file.Path == "tracked.py" && file.Size != int64(len("uncommitted contents")) {
            t.Fatal(file)
        }
    }
    found := false
    for _, ex := range p.Excluded {
        if ex.Path == "skip.log" && ex.Reason == "gitignore" && strings.Contains(ex.Rule, "*.log") {
            found = true
        }
    }
    if !found {
        t.Fatalf("no native Git exclusion reason: %+v", p.Excluded)
    }
    indexAfter, err := os.ReadFile(filepath.Join(root, ".git", "index"))
    if err != nil || !reflect.DeepEqual(indexBefore, indexAfter) {
        t.Fatal("selection changed the Git index", err)
    }
}

func TestDirectoryRulesMatchNativeGit(t *testing.T) {
    isolation(t)
    for _, patterns := range []string{
        "*.log\n!keep.log\n",
        "/root.txt\nsub/*.txt\n!sub/keep.txt\n",
        "build/*\n!build/keep.py\n",
        "gone/\n!gone/keep.py\n",
        "**/trash/**\n**/generated?.py\n",
        "\\#literal\nspace\\ name.txt\n[ab].txt\n",
        "!\n/\n!/\n*.log\n",
    } {
        t.Run(patterns, func(t *testing.T) {
            root := t.TempDir()
            for _, file := range []string{"root.txt", "sub/root.txt", "sub/keep.txt", "keep.log", "drop.log", "sub/drop.log", "build/keep.py", "build/no.py", "gone/keep.py", "src/trash/no.py", "src/generated1.py", "src/normal.py", "#literal", "space name.txt", "a.txt", "中文 文件.py"} {
                put(t, root, file, "data")
            }
            put(t, root, ".gitignore", patterns)
            put(t, root, "src/.gitignore", "normal.py\n!generated1.py\n")
            directory, err := Select(context.Background(), root, SelectOptions{})
            if err != nil {
                t.Fatal(err)
            }
            git(t, root, "init", "-q")
            native, err := Select(context.Background(), root, SelectOptions{})
            if err != nil {
                t.Fatal(err)
            }
            if !reflect.DeepEqual(names(directory), names(native)) {
                t.Fatalf("directory: %v\nGit: %v", names(directory), names(native))
            }
        })
    }
}

func TestLinksDoNotUploadExcludedOrExternalTargets(t *testing.T) {
    root := t.TempDir()
    put(t, root, "src/中文 文件.py", "data")
    put(t, root, ".env", "SECRET=not-uploaded")
    outside := t.TempDir()
    put(t, outside, "private", "private")
    for name, target := range map[string]string{"relative.py": "src/中文 文件.py", "absolute.py": filepath.Join(root, "src/中文 文件.py"), "src-link": "src", "chain.py": "relative.py", "secret": ".env", "outside": outside, "broken": "missing", "root-link": "."} {
        testutil.Symlink(t, target, filepath.Join(root, name))
    }
    p, err := Select(context.Background(), root, SelectOptions{})
    if err != nil {
        t.Fatal(err)
    }
    want := []string{"chain.py", "relative.py", "src-link", "src/中文 文件.py"}
    if !reflect.DeepEqual(names(p), want) {
        t.Fatalf("%v, excluded=%+v", names(p), p.Excluded)
    }
    for _, file := range p.Files {
        if file.Kind == "symlink" && (filepath.IsAbs(file.Link) || file.Link == "") {
            t.Fatal(file)
        }
    }
}

func TestCustomExclusionsOutputsAndPrivateState(t *testing.T) {
    root := t.TempDir()
    for _, file := range []string{"train.py", "outputs/config.py", "results/weights.pt", "private/config.toml", "x/cache/code.py", "cache/code.py", "keep.txt", "docs/readme.md"} {
        put(t, root, file, "data")
    }
    options := SelectOptions{Exclude: []string{"/cache/"}, Outputs: []string{"results/"}, PrivatePaths: []string{filepath.Join(root, "private"), filepath.Join(t.TempDir(), "unrelated")}}
    p, err := Select(context.Background(), root, options)
    if err != nil {
        t.Fatal(err)
    }
    if !reflect.DeepEqual(names(p), []string{"docs/readme.md", "keep.txt", "outputs/config.py", "train.py", "x/cache/code.py"}) {
        t.Fatal(names(p))
    }
    if options.Outputs[0] != "results/" {
        t.Fatal("mutated caller options")
    }
    for _, o := range []SelectOptions{{Exclude: []string{"!.env"}}, {Exclude: []string{"data\n!data"}}, {Outputs: []string{"."}}, {Outputs: []string{"../outside"}}, {Outputs: []string{"/tmp"}}} {
        _, err := Select(context.Background(), root, o)
        var f *fault.Error
        if !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
            t.Fatalf("%+v: %v", o, err)
        }
    }
}

func TestProjectSubdirectoryAndCanceledSelection(t *testing.T) {
    isolation(t)
    repo := t.TempDir()
    git(t, repo, "init", "-q")
    put(t, repo, "outside.py", "outside")
    put(t, repo, "project/train.py", "inside")
    git(t, repo, "add", ".")
    p, err := Select(context.Background(), filepath.Join(repo, "project"), SelectOptions{})
    if err != nil || !reflect.DeepEqual(names(p), []string{"train.py"}) {
        t.Fatal(p, err)
    }
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    if _, err := Select(ctx, t.TempDir(), SelectOptions{}); !errors.Is(err, context.Canceled) {
        t.Fatal(err)
    }
}

func TestSelectionIncludesLiteralUnusualNames(t *testing.T) {
    root := t.TempDir()
    want := []string{"#hash.py", "-flag.py", ";semi.py", "line\nbreak.py", "name[1]*?.py", "中文 文件.py"}
    if runtime.GOOS == "windows" {
        want = slices.DeleteFunc(want, func(name string) bool { return !wire.WindowsOutputPath(name) })
    }
    for _, name := range want {
        put(t, root, name, "data")
    }
    p, err := Select(context.Background(), root, SelectOptions{})
    if err != nil || !reflect.DeepEqual(names(p), want) {
        t.Fatal(names(p), err)
    }
}

func TestDisablingGitignoreKeepsLoafExclusions(t *testing.T) {
    for _, repository := range []bool{false, true} {
        root := t.TempDir()
        if repository {
            git(t, root, "init", "-q")
        }
        put(t, root, ".gitignore", "ignored/\n*.local\n")
        put(t, root, "ignored/required.py", "needed at runtime")
        put(t, root, "config.local", "configuration")
        put(t, root, ".env", "secret")
        put(t, root, ".venv/python", "environment")
        put(t, root, "outputs/result", "output")
        put(t, root, "custom.tmp", "custom")
        no := false
        plan, err := Select(context.Background(), root, SelectOptions{RespectGitignore: &no, Exclude: []string{"*.tmp"}})
        if err != nil || !reflect.DeepEqual(names(plan), []string{".gitignore", "config.local", "ignored/required.py"}) || plan.Source != "directory" || plan.Policy.RespectGitignore {
            t.Fatal(repository, names(plan), err)
        }
    }
}
