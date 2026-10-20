// Package sync selects working sources and coordinates their remote transfer.
package sync

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io/fs"
    "os"
    "os/exec"
    "path"
    "path/filepath"
    "sort"
    "strings"
    "unicode/utf8"

    gitignore "github.com/denormal/go-gitignore"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// File is a selected working file. Safe relative links retain their literal target.
type File struct {
    Path string `json:"path"`
    Kind string `json:"kind"`
    Size int64  `json:"size"`
    Link string `json:"link,omitempty"`
}

// Exclusion explains a skipped file or an entire pruned directory.
type Exclusion struct {
    Path   string `json:"path"`
    Reason string `json:"reason"`
    Rule   string `json:"rule,omitempty"`
}

// Plan describes local selection, not estimated network traffic or a snapshot.
type Plan struct {
    Root     string            `json:"root"`
    Source   string            `json:"source"`
    Policy   config.SyncPolicy `json:"policy"`
    Files    []File            `json:"files"`
    Excluded []Exclusion       `json:"excluded"`
    Bytes    int64             `json:"selected_bytes"`
}

// SelectOptions contains project exclusions and private local Loaf storage paths.
type SelectOptions struct {
    Mode             string
    RespectGitignore *bool
    Exclude          []string
    Outputs          []string
    PrivatePaths     []string
    ResultsDir       string
}

// Defaults are name-based exclusions, including secrets even when tracked by Git.
const defaultRules = `.git
.venv/
venv/
__pycache__/
.pytest_cache/
.mypy_cache/
.ruff_cache/
.cache/
.ipynb_checkpoints/
.tox/
.nox/
node_modules/
.env
.env.*
.ssh/
id_rsa
id_dsa
id_ecdsa
id_ed25519
*.pem
*.key
*.pt
*.pth
*.ckpt
*.safetensors
loaf-results/
`

type rules struct {
    base, source string
    matcher      gitignore.GitIgnore
}

func parseRules(text, base, source string) (rules, error) {
    // Git ignores empty negations and separators. The parser expects a token.
    var lines []string
    for _, line := range strings.Split(text, "\n") {
        switch strings.TrimSpace(line) {
        case "!", "/", "!/":
            line = ""
        }
        lines = append(lines, line)
    }
    var parseErr error
    matcher := gitignore.New(strings.NewReader(strings.Join(lines, "\n")), base, func(e gitignore.Error) bool {
        parseErr = e.Underlying()
        return false
    })
    return rules{base, source, matcher}, parseErr
}

func (r rules) match(rel string, dir bool) gitignore.Match {
    if r.base != "" {
        prefix := r.base + "/"
        if !strings.HasPrefix(rel, prefix) {
            return nil
        }
        rel = strings.TrimPrefix(rel, prefix)
    }
    return r.matcher.Relative(rel, dir)
}

// Select enumerates current files, preserving native Git tracking/ignore behavior.
// It never opens source contents, follows directory links, or contacts a server.
func Select(ctx context.Context, root string, o SelectOptions) (Plan, error) {
    p := Plan{Root: root, Source: "directory", Files: []File{}, Excluded: []Exclusion{}}
    actual, err := filepath.EvalSymlinks(root)
    if err != nil || !filepath.IsAbs(root) {
        return p, fault.Wrap("SOURCE_FAILED", "source_read", err)
    }
    root, p.Root = actual, actual
    p.Policy, err = config.ResolveSync(config.SyncSettings{Mode: o.Mode, RespectGitignore: o.RespectGitignore, Exclude: o.Exclude})
    if err != nil {
        return p, err
    }
    defaults, err := parseRules(defaultRules, "", "default")
    if err != nil {
        return p, err
    }
    for _, pattern := range o.Exclude {
        if strings.ContainsAny(pattern, "\x00\r\n") || strings.HasPrefix(pattern, "!") {
            return p, fault.New("BAD_CONFIG", "sync_exclude", nil)
        }
    }
    custom, err := parseRules(strings.Join(o.Exclude, "\n"), "", "sync.exclude")
    if err != nil {
        return p, fault.Wrap("BAD_CONFIG", "sync_exclude", err)
    }
    outputs := append([]string(nil), o.Outputs...)
    if len(outputs) == 0 {
        outputs = []string{"outputs"}
    }
    for i, output := range outputs {
        output = strings.TrimSuffix(output, "/")
        if !wire.ValidOutputPath(output) {
            return p, fault.New("BAD_CONFIG", "sync_outputs", nil)
        }
        outputs[i] = output
    }
    private := []string{}
    for _, name := range o.PrivatePaths {
        if canonical, err := filepath.EvalSymlinks(name); err == nil {
            name = canonical
        }
        rel, err := filepath.Rel(root, name)
        if err == nil && rel != "." && filepath.IsLocal(rel) {
            private = append(private, filepath.ToSlash(rel))
        }
    }
    results := []string{}
    if o.ResultsDir != "" {
        name := o.ResultsDir
        if canonical, err := filepath.EvalSymlinks(name); err == nil {
            name = canonical
        }
        rel, err := filepath.Rel(root, name)
        if err == nil && rel == "." {
            return p, fault.New("BAD_CONFIG", "pull_base_dir", nil)
        }
        if err == nil && filepath.IsLocal(rel) {
            results = append(results, filepath.ToSlash(rel))
        }
    }
    var candidates, parents map[string]bool
    if p.Policy.RespectGitignore && inGit(root) {
        p.Source = "git"
        cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", ".")
        out, err := cmd.Output()
        if err != nil {
            return p, fault.Wrap("SOURCE_FAILED", "source_git", err)
        }
        candidates, parents = map[string]bool{}, map[string]bool{}
        for _, name := range bytes.Split(out, []byte{0}) {
            rel := strings.TrimSuffix(string(name), "/")
            if rel == "" {
                continue
            }
            candidates[rel] = true
            for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
                parents[dir] = true
            }
        }
    }
    var links []File
    var gitExcluded []string
    exclude := func(rel, reason, rule string, dir bool) {
        if dir {
            rel += "/"
        }
        p.Excluded = append(p.Excluded, Exclusion{rel, reason, rule})
    }
    var walk func(string, []rules) error
    walk = func(dir string, scopes []rules) error {
        if err := ctx.Err(); err != nil {
            return err
        }
        if p.Policy.RespectGitignore && candidates == nil {
            ignorePath := filepath.Join(root, filepath.FromSlash(dir), ".gitignore")
            info, err := os.Lstat(ignorePath)
            if err == nil && info.Mode().IsRegular() {
                text, err := os.ReadFile(ignorePath)
                if err != nil {
                    return err
                }
                base := dir
                if base == "." {
                    base = ""
                }
                scope, err := parseRules(string(text), base, path.Join(dir, ".gitignore"))
                if err != nil {
                    return err
                }
                scopes = append(append([]rules(nil), scopes...), scope)
            } else if err != nil && !errors.Is(err, fs.ErrNotExist) {
                return err
            }
        }
        entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
        if err != nil {
            return err
        }
        for _, entry := range entries {
            if err := ctx.Err(); err != nil {
                return err
            }
            rel := path.Join(dir, entry.Name())
            if !utf8.ValidString(rel) {
                return fault.New("SOURCE_FAILED", "source_encoding", nil)
            }
            isDir := entry.IsDir()
            blocked := false
            for _, r := range []rules{defaults, custom} {
                if m := r.match(rel, isDir); m != nil && m.Ignore() {
                    exclude(rel, "excluded", r.source+": "+m.String(), isDir)
                    blocked = true
                    break
                }
            }
            if blocked {
                continue
            }
            for _, group := range []struct {
                paths  []string
                reason string
            }{{outputs, "outputs"}, {private, "private_state"}, {results, "results"}} {
                for _, item := range group.paths {
                    if rel == item || strings.HasPrefix(rel, item+"/") {
                        exclude(rel, group.reason, item, isDir)
                        blocked = true
                        break
                    }
                }
                if blocked {
                    break
                }
            }
            if blocked {
                continue
            }
            if candidates != nil {
                if isDir && candidates[rel] {
                    return fault.New("SOURCE_FAILED", "source_submodule", map[string]any{"Path": rel})
                }
                if (isDir && !parents[rel]) || (!isDir && !candidates[rel]) {
                    exclude(rel, "gitignore", "", isDir)
                    gitExcluded = append(gitExcluded, rel)
                    continue
                }
            } else {
                var matched gitignore.Match
                source := ""
                for _, scope := range scopes {
                    if m := scope.match(rel, isDir); m != nil {
                        matched, source = m, scope.source
                    }
                }
                if matched != nil && matched.Ignore() {
                    exclude(rel, "gitignore", fmt.Sprintf("%s:%d: %s", source, matched.Position().Line, matched.String()), isDir)
                    continue
                }
            }
            if isDir {
                if err := walk(rel, scopes); err != nil {
                    return err
                }
                continue
            }
            info, err := entry.Info()
            if err != nil {
                return err
            }
            if info.Mode()&os.ModeSymlink != 0 {
                links = append(links, File{Path: rel, Kind: "symlink"})
            } else if info.Mode().IsRegular() {
                p.Files = append(p.Files, File{Path: rel, Kind: "file", Size: info.Size()})
                p.Bytes += info.Size()
            } else {
                exclude(rel, "special_file", "", false)
            }
        }
        return nil
    }
    if err := walk(".", nil); err != nil {
        var f *fault.Error
        if errors.As(err, &f) {
            return p, err
        }
        return p, fault.Wrap("SOURCE_FAILED", "source_read", err)
    }
    if candidates != nil && len(gitExcluded) > 0 {
        if err := explainGit(ctx, root, gitExcluded, p.Excluded); err != nil {
            return p, fault.Wrap("SOURCE_FAILED", "source_git", err)
        }
    }
    selected := map[string]bool{}
    for _, f := range p.Files {
        selected[f.Path] = true
        for dir := path.Dir(f.Path); dir != "."; dir = path.Dir(dir) {
            selected[dir] = true
        }
    }
    portable := map[string]string{}
    for _, link := range links {
        target, err := os.Readlink(filepath.Join(root, filepath.FromSlash(link.Path)))
        if err != nil {
            return p, fault.Wrap("SOURCE_FAILED", "source_read", err)
        }
        // Absolute links are machine-specific. Preserve portable links rather
        // than adding a second transfer stage to rewrite their targets.
        if filepath.IsAbs(target) {
            exclude(link.Path, "unsafe_link", "", false)
            continue
        }
        resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(link.Path)))
        if err != nil {
            exclude(link.Path, "broken_link", "", false)
            continue
        }
        rel, err := filepath.Rel(root, resolved)
        if err != nil || !filepath.IsLocal(rel) || rel == "." {
            exclude(link.Path, "unsafe_link", "", false)
            continue
        }
        if !selected[filepath.ToSlash(rel)] {
            exclude(link.Path, "excluded_link_target", "", false)
            continue
        }
        portable[link.Path] = filepath.ToSlash(target)
    }
    for _, link := range links {
        target, ok := portable[link.Path]
        if !ok {
            continue
        }
        if !selectedLinkTarget(path.Join(path.Dir(link.Path), target), selected, portable, 0) {
            exclude(link.Path, "excluded_link_target", "", false)
            continue
        }
        link.Link = target
        p.Files = append(p.Files, link)
    }
    sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
    sort.Slice(p.Excluded, func(i, j int) bool { return p.Excluded[i].Path < p.Excluded[j].Path })
    return p, nil
}

func selectedLinkTarget(name string, selected map[string]bool, links map[string]string, depth int) bool {
    if depth > 40 || name == "." || !fs.ValidPath(name) {
        return false
    }
    if selected[name] {
        return true
    }
    for prefix := name; prefix != "."; prefix = path.Dir(prefix) {
        if target, ok := links[prefix]; ok {
            suffix := strings.TrimPrefix(name, prefix)
            return selectedLinkTarget(path.Join(path.Dir(prefix), target)+suffix, selected, links, depth+1)
        }
    }
    return false
}

func inGit(root string) bool {
    for dir := root; ; dir = filepath.Dir(dir) {
        if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
            return true
        }
        if filepath.Dir(dir) == dir {
            return false
        }
    }
}

func explainGit(ctx context.Context, root string, paths []string, excluded []Exclusion) error {
    cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "--no-index", "-z", "-v", "--stdin")
    cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
    out, err := cmd.Output()
    if err != nil {
        var exit *exec.ExitError
        if !errors.As(err, &exit) || exit.ExitCode() != 1 {
            return err
        }
    }
    fields := bytes.Split(out, []byte{0})
    reasons := map[string]string{}
    for i := 0; i+3 < len(fields); i += 4 {
        reasons[string(fields[i+3])] = string(fields[i]) + ":" + string(fields[i+1]) + ": " + string(fields[i+2])
    }
    for i := range excluded {
        if excluded[i].Reason == "gitignore" {
            excluded[i].Rule = reasons[strings.TrimSuffix(excluded[i].Path, "/")]
        }
    }
    return nil
}
