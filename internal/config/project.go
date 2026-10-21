package config

import (
    "context"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "strings"

    "github.com/elebirds/loaf/internal/fault"
)

// ProjectFile is the optional shared project configuration.
type ProjectFile struct {
    Target    string       `toml:"target"`
    RemoteDir string       `toml:"remote_dir"`
    Run       RunSettings  `toml:"run"`
    PS        PSSettings   `toml:"ps"`
    Logs      LogSettings  `toml:"logs"`
    Stop      StopSettings `toml:"stop"`
    Pull      PullSettings `toml:"pull"`
    Sync      SyncSettings `toml:"sync"`
    Outputs   struct {
        Paths []string `toml:"paths"`
    }   `toml:"outputs"`
}

// Discover chooses the nearest project TOML, Git worktree root, or current directory.
func Discover(ctx context.Context, cwd string) (string, ProjectFile, error) {
    var c ProjectFile
    root, err := filepath.EvalSymlinks(cwd)
    if err != nil {
        return "", c, err
    }
    root, err = filepath.Abs(root)
    if err != nil {
        return "", c, err
    }
    for dir := root; ; dir = filepath.Dir(dir) {
        data, err := os.ReadFile(filepath.Join(dir, "loaf.toml"))
        if err == nil {
            if err := decodeTOML(data, &c); err != nil {
                return "", c, fault.Wrap("BAD_CONFIG", "project_config", err)
            }
            if c.RemoteDir != "" && !ValidRemotePath(c.RemoteDir) {
                return "", c, fault.New("BAD_CONFIG", "invalid_remote", nil)
            }
            if _, err := ResolveSync(c.Sync); err != nil {
                return "", c, err
            }
            if _, err := ResolveWait(c.Run.Wait); err != nil {
                return "", c, err
            }
            if _, err := ResolvePSLimit(c.PS); err != nil {
                return "", c, err
            }
            if _, err := ResolveTail(c.Logs); err != nil {
                return "", c, err
            }
            if _, err := ResolveStopGrace(c.Stop); err != nil {
                return "", c, err
            }
            if _, err := ResolvePull(c.Pull); err != nil {
                return "", c, err
            }
            return dir, c, nil
        }
        if !errors.Is(err, os.ErrNotExist) {
            return "", c, fault.Wrap("BAD_CONFIG", "project_config", err)
        }
        if filepath.Dir(dir) == dir {
            break
        }
    }
    cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--show-toplevel")
    if out, err := cmd.Output(); err == nil {
        root = strings.TrimSpace(string(out))
    }
    return root, c, nil
}
