package ssh

import (
    "context"
    "os"
    "os/exec"
    "path/filepath"
    "strings"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/localpath"
)

// DiscoverTools accepts only a paired MSYS2 installation. Native Win32 OpenSSH,
// Git's SSH, Cygwin and WSL executables are never mixed into this transport.
func DiscoverTools() (Tools, error) {
    missing := func() (Tools, error) {
        f := fault.New("SSH_FAILED", "msys2_missing", nil)
        f.Hint = "msys2_hint"
        return Tools{}, f
    }
    root := os.Getenv("LOAF_MSYS2_ROOT")
    if root == "" {
        rsync, err := exec.LookPath("rsync.exe")
        if err != nil {
            return missing()
        }
        root = filepath.Dir(filepath.Dir(filepath.Dir(rsync)))
        if !strings.EqualFold(filepath.Clean(rsync), filepath.Join(root, "usr", "bin", "rsync.exe")) {
            return missing()
        }
    }
    if !filepath.IsAbs(root) {
        return missing()
    }
    if err := localpath.Supported(root); err != nil {
        return missing()
    }
    bin := filepath.Join(root, "usr", "bin")
    for _, file := range []string{filepath.Join(root, "msys2_shell.cmd"), filepath.Join(bin, "msys-2.0.dll"), filepath.Join(bin, "ssh.exe"), filepath.Join(bin, "rsync.exe"), filepath.Join(bin, "cygpath.exe")} {
        info, err := os.Stat(file)
        if err != nil || !info.Mode().IsRegular() {
            return missing()
        }
    }
    return Tools{SSH: filepath.Join(bin, "ssh.exe"), Rsync: filepath.Join(bin, "rsync.exe"), Cygpath: filepath.Join(bin, "cygpath.exe")}, nil
}

func platformSession(s connectionSession) connectionSession {
    s.target.Args = append([]string(nil), s.target.Args...)
    for i := 0; i+1 < len(s.target.Args); i++ {
        // OpenSSH forwards -F into the shell command it creates for ProxyJump.
        // DOS backslashes would be interpreted as escapes by that POSIX shell.
        if s.target.Args[i] == "-F" || s.target.Args[i] == "-i" || s.target.Args[i] == "-E" {
            s.target.Args[i+1] = filepath.ToSlash(s.target.Args[i+1])
            i++
        }
    }
    s.target.Args = append([]string{"-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no", "-o", "ForkAfterAuthentication=no"}, s.target.Args...)
    if s.env == nil {
        s.env = os.Environ()
    }
    for _, name := range []string{"MSYS2_ARG_CONV_EXCL", "MSYS2_ENV_CONV_EXCL", "MSYS", "SHELL"} {
        s.env = withoutEnv(s.env, name)
    }
    // Native symlinks must fail when privileges are missing, never become MSYS
    // link files or copies. Remote argv and the private pipe name stay literal.
    s.env = append(s.env, "MSYS2_ARG_CONV_EXCL=*", "MSYS2_ENV_CONV_EXCL=*", "MSYS=winsymlinks:nativestrict", "SHELL=/usr/bin/sh")
    return s
}

func (c *Client) localTransferPath(ctx context.Context, path string) (string, error) {
    if err := localpath.Supported(path); err != nil {
        return "", err
    }
    if c.Cygpath == "" {
        return "", fault.New("SSH_FAILED", "msys2_missing", nil)
    }
    cmd := commandContext(ctx, c.Cygpath, "-u", "--", path)
    cmd.Env = platformSession(connectionSession{}).env
    out, err := cmd.Output()
    if err != nil {
        return "", err
    }
    result := strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
    if !strings.HasPrefix(result, "/") || strings.ContainsAny(result, "\x00\r\n") {
        return "", fault.New("SSH_FAILED", "msys2_missing", nil)
    }
    return result, nil
}

func (c *Client) sshTransferPath() string { return filepath.ToSlash(c.Binary) }
