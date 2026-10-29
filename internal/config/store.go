// Package config manages user and project TOML configuration.
package config

import (
    "bytes"
    "context"
    "errors"
    "os"
    "path"
    "path/filepath"
    "regexp"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/pelletier/go-toml/v2"
    "golang.org/x/sys/unix"
)

// Target is a static SSH connection or an immutable cloud instance binding.
type Target struct {
    Destination string         `toml:"destination,omitempty" json:"destination,omitempty"`
    Args        []string       `toml:"args,omitempty" json:"args,omitempty"`
    RemoteBase  string         `toml:"remote_base" json:"remote_base"`
    Cloud       *cloud.Binding `toml:"cloud,omitempty" json:"cloud,omitempty"`
}

// Global is the user-owned configuration, independent of project TOML.
type Global struct {
    FormatVersion int                      `toml:"format_version"`
    Language      string                   `toml:"language,omitempty"`
    Sync          SyncSettings             `toml:"sync,omitempty"`
    Run           RunPreferences           `toml:"run,omitempty"`
    PS            PSSettings               `toml:"ps,omitempty"`
    Logs          LogSettings              `toml:"logs,omitempty"`
    Stop          StopSettings             `toml:"stop,omitempty"`
    Pull          PullSettings             `toml:"pull,omitempty"`
    Exec          ExecSettings             `toml:"exec,omitempty"`
    Cloud         cloud.Settings           `toml:"cloud,omitzero"`
    Resources     map[string]cloud.Profile `toml:"resources,omitempty"`
    Targets       map[string]Target        `toml:"targets"`
}

// Project is one local working directory and its personal default target.
type Project struct {
    ID     string `json:"id"`
    Root   string `json:"root"`
    Target string `json:"target,omitempty"`
}

// Store reads and writes user TOML; runtime state has a separate store.
type Store struct{ ConfigFile, StateDir string }

// New uses XDG directory overrides, or their documented home-relative defaults.
func New(home, configHome, stateHome string) *Store {
    if configHome == "" {
        configHome = filepath.Join(home, ".config")
    }
    if stateHome == "" {
        stateHome = filepath.Join(home, ".local", "state")
    }
    return &Store{filepath.Join(configHome, "loaf", "config.toml"), filepath.Join(stateHome, "loaf")}
}

func decodeTOML(data []byte, dst any) error {
    return toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(dst)
}

// Load reads configuration without creating files. Unknown formats are preserved.
func (s *Store) Load() (c Global, err error) {
    defer func() { err = locateConfig(s.ConfigFile, err) }()
    c = Global{FormatVersion: 1, Targets: map[string]Target{}}
    data, err := os.ReadFile(s.ConfigFile)
    if errors.Is(err, os.ErrNotExist) {
        return c, nil
    }
    if err != nil {
        return c, configFailure(s.ConfigFile, "config_read", err)
    }
    if err := decodeTOML(data, &c); err != nil {
        return c, configFailure(s.ConfigFile, "config_read", err)
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
    if err := validateCloud(c.Cloud, c.Resources); err != nil {
        return c, err
    }
    for _, target := range c.Targets {
        if target.Cloud != nil && !ValidCloudBinding(*target.Cloud) {
            return c, fault.New("BAD_CONFIG", "cloud_config", nil)
        }
    }
    return c, nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

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
    if target.Cloud != nil && (!ValidCloudBinding(*target.Cloud) || target.Destination != "") {
        return fault.New("BAD_CONFIG", "cloud_config", nil)
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
