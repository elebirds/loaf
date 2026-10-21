// Package config manages user configuration and project-local selections.
package config

import (
    "bytes"
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path"
    "path/filepath"
    "regexp"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/pelletier/go-toml/v2"
    "golang.org/x/sys/unix"
)

// Target is an existing SSH connection, not a provisioned machine.
type Target struct {
    Destination string   `toml:"destination" json:"destination"`
    Args        []string `toml:"args,omitempty" json:"args,omitempty"`
    RemoteBase  string   `toml:"remote_base" json:"remote_base"`
}

// Global is the user-owned configuration, independent of project TOML.
type Global struct {
    FormatVersion int               `toml:"format_version"`
    Language      string            `toml:"language,omitempty"`
    Sync          SyncSettings      `toml:"sync,omitempty"`
    Run           RunPreferences    `toml:"run,omitempty"`
    PS            PSSettings        `toml:"ps,omitempty"`
    Logs          LogSettings       `toml:"logs,omitempty"`
    Stop          StopSettings      `toml:"stop,omitempty"`
    Pull          PullSettings      `toml:"pull,omitempty"`
    Exec          ExecSettings      `toml:"exec,omitempty"`
    Targets       map[string]Target `toml:"targets"`
}

// Project is one local working directory and its personal default target.
type Project struct {
    ID     string `json:"id"`
    Root   string `json:"root"`
    Target string `json:"target,omitempty"`
}

type index struct {
    FormatVersion int                `json:"format_version"`
    Projects      map[string]Project `json:"projects"`
}

// Store uses private user directories and atomic, locked updates.
type Store struct{ ConfigFile, IndexFile string }

// New uses XDG directory overrides, or their documented home-relative defaults.
func New(home, configHome, stateHome string) *Store {
    if configHome == "" {
        configHome = filepath.Join(home, ".config")
    }
    if stateHome == "" {
        stateHome = filepath.Join(home, ".local", "state")
    }
    return &Store{filepath.Join(configHome, "loaf", "config.toml"), filepath.Join(stateHome, "loaf", "index.json")}
}

func decodeTOML(data []byte, dst any) error {
    return toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(dst)
}

// Load reads configuration without creating files. Unknown formats are preserved.
func (s *Store) Load() (Global, error) {
    c := Global{FormatVersion: 1, Targets: map[string]Target{}}
    data, err := os.ReadFile(s.ConfigFile)
    if errors.Is(err, os.ErrNotExist) {
        return c, nil
    }
    if err != nil {
        return c, fault.Wrap("BAD_CONFIG", "config_read", err)
    }
    if err := decodeTOML(data, &c); err != nil {
        return c, fault.Wrap("BAD_CONFIG", "config_read", err)
    }
    if c.FormatVersion != 1 {
        return c, fault.New("BAD_CONFIG", "unknown_format", nil)
    }
    if c.Targets == nil {
        c.Targets = map[string]Target{}
    }
    if _, err := ResolveSync(c.Sync); err != nil {
        return c, err
    }
    if _, err := ResolveWait(c.Run.Wait); err != nil {
        return c, err
    }
    if _, err := ResolvePSLimit(c.PS); err != nil {
        return c, err
    }
    if _, err := ResolveTail(c.Logs); err != nil {
        return c, err
    }
    if _, err := ResolveStopGrace(c.Stop); err != nil {
        return c, err
    }
    if _, err := ResolvePull(c.Pull); err != nil {
        return c, err
    }
    if _, err := ResolveExecCWD(c.Exec); err != nil {
        return c, err
    }
    return c, nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var projectIDPattern = regexp.MustCompile(`^p_[0-9a-f]{24}$`)

// ValidName restricts aliases to unambiguous CLI identifiers.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// ValidRemotePath accepts absolute or home-relative directories, without traversal.
func ValidRemotePath(path string) bool {
    if !(strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~/")) || strings.ContainsAny(path, "\x00\r\n") {
        return false
    }
    for _, part := range strings.Split(path, "/") {
        if part == ".." {
            return false
        }
    }
    return cleanRemote(path)
}

func cleanRemote(value string) bool {
    cleaned := path.Clean(value)
    return cleaned != "/" && cleaned != "~"
}

// Add saves a new alias. Existing aliases require an explicit replacement.
func (s *Store) Add(ctx context.Context, name string, target Target, replace bool) error {
    if !ValidName(name) || !ValidRemotePath(target.RemoteBase) {
        return fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    return locked(ctx, s.ConfigFile, func() error {
        c, err := s.Load()
        if err != nil {
            return err
        }
        if _, ok := c.Targets[name]; ok && !replace {
            return fault.New("BAD_CONFIG", "target_exists", map[string]any{"Target": name})
        }
        c.Targets[name] = target
        data, err := toml.Marshal(c)
        if err != nil {
            return err
        }
        return atomicfile.Write(s.ConfigFile, data)
    })
}

func (s *Store) loadIndex() (index, error) {
    i := index{1, map[string]Project{}}
    data, err := os.ReadFile(s.IndexFile)
    if errors.Is(err, os.ErrNotExist) {
        return i, nil
    }
    if err != nil {
        return i, err
    }
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    if err := decoder.Decode(&i); err != nil {
        return i, err
    }
    if decoder.Decode(new(any)) != io.EOF {
        return i, fault.New("BAD_CONFIG", "config_read", nil)
    }
    if i.FormatVersion != 1 {
        return i, fault.New("BAD_CONFIG", "unknown_format", nil)
    }
    if i.Projects == nil {
        i.Projects = map[string]Project{}
    }
    for root, p := range i.Projects {
        if p.Root != root || !filepath.IsAbs(root) || !projectIDPattern.MatchString(p.ID) || (p.Target != "" && !ValidName(p.Target)) {
            return i, fault.New("BAD_CONFIG", "config_read", nil)
        }
    }
    return i, nil
}

// Select returns a stable project identity, optionally saving a default target.
func (s *Store) Select(ctx context.Context, root, target string) (Project, error) {
    var result Project
    if !filepath.IsAbs(root) || (target != "" && !ValidName(target)) {
        return result, fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    err := locked(ctx, s.IndexFile, func() error {
        i, err := s.loadIndex()
        if err != nil {
            return err
        }
        result = i.Projects[root]
        if result.ID == "" {
            var id [12]byte
            if _, err := rand.Read(id[:]); err != nil {
                return err
            }
            result = Project{ID: "p_" + hex.EncodeToString(id[:]), Root: root}
        }
        if target != "" {
            result.Target = target
        }
        i.Projects[root] = result
        data, err := json.MarshalIndent(i, "", "    ")
        if err != nil {
            return err
        }
        return atomicfile.Write(s.IndexFile, append(data, '\n'))
    })
    if err != nil {
        var f *fault.Error
        if errors.As(err, &f) {
            return result, err
        }
        return result, fault.Wrap("BAD_CONFIG", "state_write", err)
    }
    return result, nil
}

// Existing returns a project's saved selection without creating state.
func (s *Store) Existing(root string) (Project, error) {
    i, err := s.loadIndex()
    if err != nil {
        return Project{}, fault.Wrap("BAD_CONFIG", "config_read", err)
    }
    return i.Projects[root], nil
}

// Resolve applies command-line, personal project, then shared TOML precedence.
func Resolve(explicit string, personal Project, file ProjectFile, global Global) (string, Target, error) {
    name := explicit
    if name == "" {
        name = personal.Target
    }
    if name == "" {
        name = file.Target
    }
    if name == "" {
        return "", Target{}, fault.New("BAD_CONFIG", "target_required", nil)
    }
    t, ok := global.Targets[name]
    if !ok {
        return "", Target{}, fault.New("BAD_CONFIG", "target_unknown", map[string]any{"Target": name})
    }
    if !ValidName(name) || !ValidRemotePath(t.RemoteBase) {
        return "", Target{}, fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    return name, t, nil
}

func locked(ctx context.Context, path string, fn func() error) error {
    if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
        return err
    }
    f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
    if err != nil {
        return err
    }
    defer f.Close()
    for {
        err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
        if err == nil {
            break
        }
        if !errors.Is(err, unix.EWOULDBLOCK) {
            return err
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(10 * time.Millisecond):
        }
    }
    defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
    return fn()
}
