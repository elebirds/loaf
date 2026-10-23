package ssh

import (
    "bytes"
    "context"
    "errors"
    "io/fs"
    "net/url"
    "os"
    "os/exec"
    "path"
    "regexp"
    "sort"
    "strings"
    "syscall"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// Rsync invokes the system sender with native OpenSSH and rsync-path arguments.
type Rsync struct {
    Client *Client
    Binary string
}

// SyncInput is transient local invocation data, not a remote manifest or record.
type SyncInput struct {
    Root, ProjectID, Mode string
    RunID                 string
    Paths                 []string
}

// Transfer counts transferred file contents and changed link targets, excluding
// directory and timestamp/permission-only updates. It does not estimate bytes.
type Transfer struct {
    ChangedFiles int `json:"changed_files"`
    // Retried means earlier transfer effects cannot be counted from the last
    // successful rsync process alone. It is not a completed-file estimate.
    Retried bool `json:"-"`
}

// Sync mirrors or updates selected working files. Only rsync carries the payload.
func (r Rsync) Sync(ctx context.Context, target config.Target, runner, root string, input SyncInput, batch bool) (result Transfer, err error) {
    var trace *Trace
    if r.Client != nil {
        trace = r.Client.Trace
    }
    finish := trace.stage("sync")
    defer func() { finish(err) }()
    defer func() { err = fault.At(err, "sync") }()
    if r.Client == nil || r.Client.Binary == "" {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    if r.Binary == "" {
        return result, fault.New("SYNC_FAILED", "sync_rsync_missing", nil)
    }
    session, err := r.Client.prepare(ctx, target, batch)
    if err != nil {
        return result, err
    }
    defer session.close()
    target, batch = session.target, session.batch
    filters, err := sourceFilters(input.Paths)
    if err != nil {
        return result, err
    }
    shell, destination := r.remoteShell(target, batch)
    words := []string{runner, "__receive", wire.Version, input.Root, input.ProjectID}
    if input.RunID != "" {
        words = append(words, "--run", input.RunID)
    }
    receiver, err := r.receiverCommand(ctx, words)
    if err != nil {
        return result, err
    }
    args := []string{"-rlptc", "--safe-links", "--from0", "--include-from=-", "--exclude=*", "--out-format=LOAF_CHANGE:%i", "--rsync-path=" + receiver, "-e", shell}
    switch input.Mode {
    case "mirror":
        args = append(args, "--delete", "--delete-excluded")
    case "update":
    default:
        return result, fault.New("BAD_CONFIG", "sync_mode", nil)
    }
    args = append(args, "--", root+"/", destination+":cache")
    cmd := exec.CommandContext(ctx, r.Binary, args...)
    cmd.Env = session.env
    cmd.Stdin = strings.NewReader(filters)
    var output, diagnostic bytes.Buffer
    cmd.Stdout, cmd.Stderr = &output, &diagnostic
    // This group contains only this sender and its SSH transport, not any run.
    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    cmd.Cancel = func() error {
        err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
        if errors.Is(err, syscall.ESRCH) {
            return os.ErrProcessDone
        }
        return err
    }
    err = cmd.Run()
    if ctx.Err() != nil {
        f := fault.Wrap("SYNC_CANCELED", "sync_canceled", ctx.Err())
        f.Hint = "sync_retry"
        return result, f
    }
    if err != nil {
        return result, syncFailure(err, diagnostic.String())
    }
    for _, line := range strings.Split(output.String(), "\n") {
        item, ok := strings.CutPrefix(line, "LOAF_CHANGE:")
        if ok && len(item) >= 2 && strings.ContainsRune("<>c", rune(item[0])) && (item[1] == 'f' || item[1] == 'L') {
            result.ChangedFiles++
        }
    }
    return result, nil
}

func (r Rsync) receiverCommand(ctx context.Context, words []string) (string, error) {
    version, err := exec.CommandContext(ctx, r.Binary, "--version").CombinedOutput()
    if err != nil {
        return "", syncFailure(err, string(version))
    }
    openrsync := bytes.HasPrefix(version, []byte("openrsync:"))
    for i := range words {
        words[i] = Quote(words[i])
        if openrsync {
            // openrsync tokenizes rsync-path before invoking SSH. Preserve one
            // literal quoting layer for the remote shell; GNU passes it intact.
            words[i] = quoteRsync(words[i])
        }
    }
    return strings.Join(words, " "), nil
}

func (r Rsync) remoteShell(target config.Target, batch bool) (string, string) {
    destination := target.Destination
    if strings.HasPrefix(destination, "ssh://") {
        // SSH URLs are accepted by registration but not rsync's host:path syntax.
        // Append URL defaults: OpenSSH gives earlier explicit -p/-l options priority.
        u, _ := url.Parse(destination) // already validated
        destination = u.Hostname()
        if strings.Contains(destination, ":") {
            destination = "[" + destination + "]"
        }
        if u.Port() != "" {
            target.Args = append(append([]string(nil), target.Args...), "-p", u.Port())
        }
        if u.User != nil {
            target.Args = append(append([]string(nil), target.Args...), "-l", u.User.Username())
        }
    } else if at := strings.LastIndexByte(destination, '@'); at >= 0 {
        // GNU rsync extracts user@host into -l user after the -e command,
        // which would place -l after our SSH -- terminator. Put the user in
        // SSH's options ourselves and pass only the host to rsync.
        target.Args = append(append([]string(nil), target.Args...), "-l", destination[:at])
        destination = destination[at+1:]
    }
    argv := append([]string{r.Client.Binary}, options(target, batch)...)
    argv = append(argv, "--")
    for i := range argv {
        argv[i] = quoteRsync(argv[i])
    }
    return strings.Join(argv, " "), destination
}

// rsync's command tokenizer uses doubled quotes, not shell backslash escapes.
func quoteRsync(word string) string {
    return `"` + strings.ReplaceAll(word, `"`, `""`) + `"`
}

// Explicit anchored includes plus the final exclude make the selected set a
// native rsync mirror. files-from alone cannot delete unlisted siblings.
func sourceFilters(paths []string) (string, error) {
    allowed := map[string]bool{}
    for _, name := range paths {
        if name == "." || !fs.ValidPath(name) || strings.ContainsRune(name, 0) {
            return "", fault.New("SOURCE_FAILED", "source_read", nil)
        }
        allowed[name] = true
        for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
            allowed[dir+"/"] = true
        }
    }
    names := make([]string, 0, len(allowed))
    for name := range allowed {
        names = append(names, name)
    }
    sort.Strings(names)
    escape := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[")
    var rules strings.Builder
    for _, name := range names {
        // '/' anchors literal # and ; names; NUL preserves newlines and spaces.
        // rsync treats backslashes literally when a rule has no wildcard token.
        if strings.ContainsAny(name, "*?[") {
            name = escape.Replace(name)
        }
        rules.WriteString("/" + name)
        rules.WriteByte(0)
    }
    return rules.String(), nil
}

var syncCode = regexp.MustCompile(`(?m)^LOAF_SYNC_ERROR:([A-Z_]+)$`)

func syncFailure(cause error, diagnostic string) error {
    code, message := "SYNC_FAILED", "sync_failed"
    if match := syncCode.FindStringSubmatch(diagnostic); match != nil {
        code = match[1]
        switch code {
        case "PROJECT_BUSY":
            message = "sync_busy"
        case "PROJECT_UNOWNED":
            message = "sync_unowned"
        case "PROJECT_MISMATCH":
            message = "sync_mismatch"
        case "UNSAFE_PATH":
            message = "sync_unsafe"
        case "BAD_FORMAT", "VERSION_MISMATCH", "BAD_RECEIVER":
            message = "sync_version"
        case "WRITE_FAILED":
            message = "sync_write"
        }
    }
    f := fault.Wrap(code, message, cause)
    f.Params = map[string]any{"Detail": strings.TrimSpace(diagnostic)}
    f.Hint = "sync_retry"
    if code == "PROJECT_UNOWNED" || code == "PROJECT_MISMATCH" || code == "UNSAFE_PATH" {
        f.Hint = "sync_directory_hint"
    }
    return f
}
