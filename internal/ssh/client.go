package ssh

import (
    "bytes"
    "context"
    "io"
    "os/exec"
    "strconv"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// Client invokes a specific system SSH executable. Streams are supplied by the caller.
type Client struct {
    Binary            string
    Resolve           ResolveConnection
    AskpassExecutable string
    SensitiveEnv      []string
    Connections       *Connections
    Trace             *Trace
}

// Execute invokes a fixed remote operation with separately encoded input.
func (c *Client) Execute(ctx context.Context, target config.Target, command string, input io.Reader, noninteractive bool) (out []byte, err error) {
    finish := c.Trace.stage("ssh_operation")
    defer func() { finish(err) }()
    cloudTarget := target.Cloud != nil
    session, err := c.prepare(ctx, target, noninteractive)
    if err != nil {
        return nil, err
    }
    defer session.close()
    target, noninteractive = session.target, session.batch
    cmd := exec.CommandContext(ctx, c.Binary, arguments(target, command, noninteractive)...)
    cmd.Env = session.env
    cmd.Stdin = input
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    output, err := cmd.Output()
    if err != nil {
        f := fault.Wrap("SSH_FAILED", "ssh_failed", session.transportError(err))
        f.Hint = "ssh_hint"
        if cloudTarget {
            f.Hint = "cloud_ssh_hint"
        }
        // Preserve the original diagnostic without requiring callers to parse English.
        f.Params = map[string]any{"Detail": strings.TrimSpace(stderr.String())}
        return output, f
    }
    return output, nil
}

func arguments(target config.Target, command string, noninteractive bool) []string {
    return append(options(target, noninteractive), "--", target.Destination, command)
}

func options(target config.Target, noninteractive bool) []string {
    args := []string{"-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-T"}
    if noninteractive {
        args = append(args, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes")
    }
    return append(args, target.Args...)
}

// Tool is an observed executable and its original version output.
type Tool struct {
    Path    string `json:"path"`
    Version string `json:"version,omitempty"`
}

// Report contains observations, not an inferred training-environment guarantee.
type Report struct {
    OS                 string               `json:"os"`
    Arch               string               `json:"arch"`
    RemoteDir          string               `json:"remote_dir"`
    Writable           bool                 `json:"directory_writable"`
    DiskAvailableBytes int64                `json:"disk_available_bytes"`
    Local              map[string]Tool      `json:"local_tools"`
    Remote             map[string]Tool      `json:"remote_tools"`
    GPU                string               `json:"gpu,omitempty"`
    Resources          *ResourceObservation `json:"resources,omitempty"`
    Issues             []string             `json:"issues"`
}

// Check observes the remote environment without installing tools or creating directories.
func (c *Client) Check(ctx context.Context, target config.Target, remoteDir string, noninteractive bool) (Report, error) {
    return c.check(ctx, target, remoteDir, noninteractive, false)
}

// Preflight checks execution prerequisites without probing Python or GPU tools.
func (c *Client) Preflight(ctx context.Context, target config.Target, remoteDir string, batch bool) (Report, error) {
    return c.check(ctx, target, remoteDir, batch, true)
}

func (c *Client) check(ctx context.Context, target config.Target, remoteDir string, noninteractive, minimal bool) (r Report, err error) {
    finish := c.Trace.stage("preflight")
    defer func() { finish(err) }()
    r = Report{Local: map[string]Tool{}, Remote: map[string]Tool{}, Issues: []string{}}
    if !config.ValidRemotePath(remoteDir) {
        return r, fault.New("BAD_CONFIG", "invalid_remote", nil)
    }
    var localVersion bytes.Buffer
    cmd := exec.CommandContext(ctx, c.Binary, "-V")
    cmd.Stdout = &localVersion
    cmd.Stderr = &localVersion
    if err := cmd.Run(); err != nil {
        return r, fault.Wrap("SSH_FAILED", "ssh_failed", err)
    }
    r.Local["ssh"] = Tool{c.Binary, strings.TrimSpace(localVersion.String())}
    if path, err := exec.LookPath("rsync"); err == nil {
        out, err := exec.CommandContext(ctx, path, "--version").Output()
        if err == nil {
            r.Local["rsync"] = Tool{path, strings.SplitN(string(out), "\n", 2)[0]}
        } else {
            r.Issues = append(r.Issues, "LOCAL_RSYNC_FAILED")
        }
    } else {
        r.Issues = append(r.Issues, "LOCAL_RSYNC_MISSING")
    }
    script := probeScript
    if minimal {
        script = "minimal=yes\n" + script
    }
    out, err := c.executeRead(ctx, target, "/bin/sh -s -- "+Quote(remoteDir), []byte(script), noninteractive)
    if err != nil {
        return r, fault.At(err, "preflight")
    }
    fields := strings.Split(string(out), "\x00")
    if len(fields) != 22 || fields[0] != "loaf-probe-2" {
        return r, fault.New("SSH_FAILED", "probe_failed", nil)
    }
    r.OS, r.Arch, r.RemoteDir = fields[1], fields[2], fields[3]
    r.Writable = fields[4] == "yes"
    kb, err := strconv.ParseInt(fields[5], 10, 64)
    if err != nil || kb < 0 {
        return r, fault.New("SSH_FAILED", "probe_failed", nil)
    }
    r.DiskAvailableBytes = kb * 1024
    for i, name := range []string{"rsync", "python", "uv", "nvidia-smi", "nvcc"} {
        r.Remote[name] = Tool{fields[6+2*i], fields[7+2*i]}
    }
    r.GPU = fields[16]
    if !minimal {
        r.Resources = parseResourceObservation(fields[17:21])
    }
    if r.OS != "Linux" {
        r.Issues = append(r.Issues, "REMOTE_OS_UNSUPPORTED")
    }
    if r.Arch != "x86_64" && r.Arch != "aarch64" {
        r.Issues = append(r.Issues, "REMOTE_ARCH_UNSUPPORTED")
    }
    if !r.Writable {
        r.Issues = append(r.Issues, "REMOTE_DIRECTORY_UNWRITABLE")
    }
    if r.Remote["rsync"].Path == "" {
        r.Issues = append(r.Issues, "REMOTE_RSYNC_MISSING")
    }
    if len(r.Issues) > 0 {
        return r, fault.New("CHECK_FAILED", "check_failed", nil)
    }
    return r, nil
}

const probeScript = `set -eu
remote=$1
case "$remote" in '~/'*) remote="$HOME/${remote#\~/}" ;; esac
case "$remote" in /|"$HOME"|"$HOME"/) exit 64 ;; esac
existing=$remote
while [ ! -e "$existing" ]; do existing=$(dirname "$existing"); done
writable=no
if [ -d "$existing" ] && [ -w "$existing" ] && [ -x "$existing" ]; then writable=yes; fi
available=$(df -Pk "$existing" | awk 'END {print $4}')
emit() { printf '%s\000' "$1"; }
emit loaf-probe-2
emit "$(uname -s)"; emit "$(uname -m)"; emit "$remote"; emit "$writable"; emit "$available"
rsync=$(command -v rsync || true); emit "$rsync"; emit "$(if [ -n "$rsync" ]; then "$rsync" --version | head -n 1; fi)"
if [ "${minimal:-no}" = yes ]; then i=0; while [ "$i" -lt 13 ]; do emit ""; i=$((i+1)); done; exit 0; fi
python=$(command -v python3 || command -v python || true); emit "$python"; emit "$(if [ -n "$python" ]; then "$python" --version 2>&1 || true; fi)"
uv=$(command -v uv || true); emit "$uv"; emit "$(if [ -n "$uv" ]; then "$uv" --version 2>&1 || true; fi)"
gpu=$(command -v nvidia-smi || true); emit "$gpu"; emit "$(if [ -n "$gpu" ]; then "$gpu" --version 2>&1 | head -n 1 || true; fi)"
cuda=$(command -v nvcc || true); emit "$cuda"; emit "$(if [ -n "$cuda" ]; then "$cuda" --version 2>&1 || true; fi)"
emit "$(if [ -n "$gpu" ]; then "$gpu" --query-gpu=name,driver_version,memory.total --format=csv,noheader 2>&1 || true; fi)"
emit "$(if command -v getconf >/dev/null 2>&1; then getconf _NPROCESSORS_ONLN 2>/dev/null || true; elif [ -r /proc/cpuinfo ]; then awk '/^processor[[:space:]]*:/ {n++} END {if (n>0) print n}' /proc/cpuinfo; fi)"
emit "$(if [ -r /proc/meminfo ]; then awk '/^MemTotal:/ {print $2; exit}' /proc/meminfo; fi)"
emit "$(if [ -r /proc/meminfo ]; then awk '/^MemAvailable:/ {print $2; exit}' /proc/meminfo; fi)"
emit "$(if [ -n "$gpu" ]; then "$gpu" --query-gpu=index,name,memory.total,memory.used,utilization.gpu --format=csv,noheader,nounits 2>/dev/null || true; fi)"
`
