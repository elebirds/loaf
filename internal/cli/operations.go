package cli

import (
    "context"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "strconv"
    "sync"
    "unicode/utf8"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/spf13/cobra"
)

// MachineOperations is the foreground OpenSSH boundary, independent of run state.
type MachineOperations interface {
    Exec(context.Context, config.Target, ssh.ExecInput, io.Reader, io.Writer, io.Writer) (ssh.ExecResult, error)
    Interactive(context.Context, config.Target, io.Reader, io.Writer, io.Writer) error
    Forward(context.Context, config.Target, int, int, bool, io.Writer) error
}

type execResult struct {
    Type   string `json:"type"`
    Target string `json:"target"`
    ssh.ExecResult
}

type execChunk struct {
    Type     string `json:"type"`
    Stream   string `json:"stream"`
    Offset   int64  `json:"offset"`
    Encoding string `json:"encoding"`
    Data     string `json:"data"`
}

// execOutput bounds every NDJSON chunk while preserving arbitrary program bytes.
// One shared lock serializes the encoder; offsets are independent per stream.
type execOutput struct {
    encoder *json.Encoder
    lock    *sync.Mutex
    stream  string
    offset  int64
}

func (w *execOutput) Write(data []byte) (int, error) {
    w.lock.Lock()
    defer w.lock.Unlock()
    written := 0
    for len(data) > 0 {
        n := min(len(data), 32<<10)
        chunk := data[:n]
        event := execChunk{Type: "chunk", Stream: w.stream, Offset: w.offset, Encoding: "utf-8", Data: string(chunk)}
        if !utf8.Valid(chunk) {
            event.Encoding = "base64"
            event.Data = base64.StdEncoding.EncodeToString(chunk)
        }
        if err := w.encoder.Encode(envelope{SchemaVersion: 1, OK: true, Data: event}); err != nil {
            return written, err
        }
        w.offset += int64(n)
        written += n
        data = data[n:]
    }
    return written, nil
}

func newMachineCommands(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any), streamed *bool) []*cobra.Command {
    exec := &cobra.Command{Use: "exec " + l.Text("exec_args"), Short: l.Text("exec_short")}
    var cwd string
    exec.Flags().StringVar(&cwd, "cwd", "", l.Text("exec_flag_cwd"))
    exec.Args = func(cmd *cobra.Command, args []string) error {
        if len(args) == 0 || cmd.ArgsLenAtDash() != 0 {
            return fault.New("BAD_CONFIG", "exec_command", nil)
        }
        return nil
    }
    exec.RunE = func(cmd *cobra.Command, args []string) error {
        name, machine, file, err := operationTarget(cmd.Context(), o, cfg, *target)
        if err != nil {
            return err
        }
        settings := []config.ExecSettings{cfg.Exec, file.Exec}
        if cmd.Flags().Changed("cwd") {
            settings = append(settings, config.ExecSettings{CWD: &cwd})
        }
        directory, err := config.ResolveExecCWD(settings...)
        if err != nil {
            return err
        }
        if o.Operations == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        stdout, stderr := o.Out, o.Err
        if jsonMode {
            *streamed = true
            encoder := json.NewEncoder(o.Out)
            lock := &sync.Mutex{}
            stdout = &execOutput{encoder: encoder, lock: lock, stream: "stdout"}
            stderr = &execOutput{encoder: encoder, lock: lock, stream: "stderr"}
        }
        observed, err := o.Operations.Exec(cmd.Context(), machine, ssh.ExecInput{Args: args, CWD: directory, Batch: jsonMode || !o.Interactive}, o.In, stdout, stderr)
        end := execResult{Type: "end", Target: name, ExecResult: observed}
        if err != nil {
            end.Type = "error"
            result(end)
            return err
        }
        if jsonMode {
            return json.NewEncoder(o.Out).Encode(envelope{SchemaVersion: 1, OK: true, Data: end})
        }
        return nil
    }
    login := &cobra.Command{Use: "ssh", Short: l.Text("interactive_short"), Args: arguments(0, 0)}
    login.RunE = func(cmd *cobra.Command, _ []string) error {
        jsonMode, _ := cmd.Flags().GetBool("json")
        if jsonMode {
            return fault.New("BAD_CONFIG", "interactive_json", nil)
        }
        if !o.Interactive {
            return fault.New("BAD_CONFIG", "interactive_terminal", nil)
        }
        _, machine, _, err := operationTarget(cmd.Context(), o, cfg, *target)
        if err != nil {
            return err
        }
        if o.Operations == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        return o.Operations.Interactive(cmd.Context(), machine, o.In, o.Out, o.Err)
    }
    forward := &cobra.Command{Use: "forward " + l.Text("forward_port_arg"), Short: l.Text("forward_short"), Args: arguments(1, 1)}
    var local int
    forward.Flags().IntVar(&local, "local-port", 0, l.Text("forward_flag_local_port"))
    forward.Flags().Lookup("local-port").DefValue = ""
    forward.RunE = func(cmd *cobra.Command, args []string) error {
        jsonMode, _ := cmd.Flags().GetBool("json")
        if jsonMode {
            return fault.New("BAD_CONFIG", "forward_json", nil)
        }
        remote, err := strconv.Atoi(args[0])
        if err != nil || remote < 1 || remote > 65535 {
            return fault.New("BAD_CONFIG", "forward_port", nil)
        }
        if !cmd.Flags().Changed("local-port") {
            local = remote
        }
        if local < 1 || local > 65535 {
            return fault.New("BAD_CONFIG", "forward_port", nil)
        }
        name, machine, _, err := operationTarget(cmd.Context(), o, cfg, *target)
        if err != nil {
            return err
        }
        if o.Operations == nil {
            return fault.New("SSH_FAILED", "ssh_missing", nil)
        }
        fmt.Fprintln(o.Err, l.Text("forward_opening", map[string]any{"Target": name, "Value": fmt.Sprintf("127.0.0.1:%d -> 127.0.0.1:%d", local, remote)}))
        return o.Operations.Forward(cmd.Context(), machine, local, remote, !o.Interactive, o.Err)
    }
    return []*cobra.Command{exec, login, forward}
}

func operationTarget(ctx context.Context, o Options, cfg config.Global, explicit string) (string, config.Target, config.ProjectFile, error) {
    root, file, err := config.Discover(ctx, o.CWD)
    if err != nil {
        return "", config.Target{}, file, err
    }
    personal, err := o.Store.Existing(root)
    if err != nil {
        return "", config.Target{}, file, err
    }
    name, machine, err := config.Resolve(explicit, personal, file, cfg)
    return name, machine, file, err
}
