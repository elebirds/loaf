// Package cli owns argument parsing and localized or structured presentation.
package cli

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "path"
    "runtime/debug"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/spf13/cobra"
    "github.com/spf13/pflag"
    "golang.org/x/text/width"
)

// Checker is the environment capability consumed by the check command.
type Checker interface {
    Check(context.Context, config.Target, string, bool) (ssh.Report, error)
}

// RunnerVerifier is the optional, explicit runner deployment check.
type RunnerVerifier interface {
    Verify(context.Context, config.Target, string, bool) (execution.RunnerInfo, error)
}

// Options are assembled by the executable, not looked up inside command handlers.
type Options struct {
    Store       *config.Store
    Checker     Checker
    Runner      RunnerVerifier
    Syncer      Synchronizer
    Cleaner     RunCleaner
    Puller      OutputPuller
    Runs        *execution.Service
    Operations  MachineOperations
    CWD         string
    Env         map[string]string
    In          io.Reader
    Out, Err    io.Writer
    Interactive bool
}

type errorView struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Hint    string `json:"hint,omitempty"`
    Detail  string `json:"detail,omitempty"`
}
type envelope struct {
    SchemaVersion int        `json:"schema_version"`
    OK            bool       `json:"ok"`
    Data          any        `json:"data,omitempty"`
    Error         *errorView `json:"error,omitempty"`
}
type addResult struct {
    Target      string `json:"target"`
    Destination string `json:"destination"`
    RemoteBase  string `json:"remote_base"`
}
type useResult struct {
    Target    string `json:"target"`
    ProjectID string `json:"project_id"`
    Root      string `json:"root"`
}
type checkResult struct {
    Target    string                `json:"target"`
    ProjectID string                `json:"project_id"`
    Report    ssh.Report            `json:"report"`
    Runner    *execution.RunnerInfo `json:"runner,omitempty"`
}

// Execute runs one command and returns the public operation exit code.
func Execute(ctx context.Context, args []string, o Options) int {
    jsonMode, preferred, earlyErr := earlyOptions(args)
    cfg, cfgErr := o.Store.Load()
    if preferred == "" {
        preferred = o.Env["LOAF_LANG"]
    }
    if preferred == "" {
        preferred = cfg.Language
    }
    if preferred == "" {
        preferred = "auto"
    }
    l, langErr := i18n.New(preferred, o.Env)
    var result any
    streamed := false
    var target string
    root := &cobra.Command{Use: "loaf", Short: l.Text("root_short"), SilenceErrors: true, SilenceUsage: true, DisableAutoGenTag: true}
    root.CompletionOptions.DisableDefaultCmd = true
    root.SetIn(o.In)
    root.SetOut(o.Out)
    root.SetErr(o.Err)
    root.SetArgs(args)
    root.PersistentFlags().String("lang", "auto", l.Text("flag_lang"))
    root.PersistentFlags().Bool("json", false, l.Text("flag_json"))
    root.PersistentFlags().StringVar(&target, "target", "", l.Text("flag_target"))
    root.PersistentFlags().BoolP("help", "h", false, l.Text("flag_help"))
    root.SetFlagErrorFunc(func(_ *cobra.Command, _ error) error {
        f := fault.New("BAD_CONFIG", "invalid_flags", nil)
        f.Hint = "help_hint"
        return f
    })
    root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
        var b bytes.Buffer
        printHelp(&b, cmd, l)
        if jsonMode {
            result = map[string]string{"help": b.String()}
        } else {
            fmt.Fprint(o.Out, b.String())
        }
    })
    root.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
    root.Args = arguments(0, 0)
    add := &cobra.Command{Use: "add " + l.Text("name_arg"), Short: l.Text("add_short"), Args: arguments(1, 1)}
    var connection, remoteBase string
    var replace bool
    add.Flags().StringVar(&connection, "ssh", "", l.Text("flag_ssh"))
    add.Flags().StringVar(&remoteBase, "remote-dir", "~/.local/share/loaf/projects", l.Text("flag_remote"))
    add.Flags().BoolVar(&replace, "replace", false, l.Text("flag_replace"))
    add.RunE = func(cmd *cobra.Command, args []string) error {
        if connection == "" {
            if jsonMode || !o.Interactive {
                return fault.New("BAD_CONFIG", "ssh_required", nil)
            }
            fmt.Fprint(o.Err, l.Text("ssh_prompt"))
            line, err := bufio.NewReader(o.In).ReadString('\n')
            if err != nil && !errors.Is(err, io.EOF) {
                return err
            }
            connection = strings.TrimSpace(line)
        }
        t, err := ssh.ParseConnection(connection)
        if err != nil {
            return err
        }
        t.RemoteBase = remoteBase
        if err := o.Store.Add(cmd.Context(), args[0], t, replace); err != nil {
            return err
        }
        result = addResult{args[0], t.Destination, t.RemoteBase}
        return nil
    }
    use := &cobra.Command{Use: "use " + l.Text("name_arg"), Short: l.Text("use_short"), Args: arguments(1, 1)}
    use.RunE = func(cmd *cobra.Command, args []string) error {
        if _, ok := cfg.Targets[args[0]]; !ok {
            return fault.New("BAD_CONFIG", "target_unknown", map[string]any{"Target": args[0]})
        }
        root, _, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        p, err := o.Store.Select(cmd.Context(), root, args[0])
        if err != nil {
            return err
        }
        result = useResult{args[0], p.ID, p.Root}
        return nil
    }
    check := &cobra.Command{Use: "check", Short: l.Text("check_short"), Args: arguments(0, 0)}
    var timeout time.Duration
    var verifyRunner bool
    check.Flags().BoolVar(&verifyRunner, "runner", false, l.Text("flag_runner"))
    check.Flags().DurationVar(&timeout, "timeout", 20*time.Second, l.Text("flag_timeout"))
    check.RunE = func(cmd *cobra.Command, _ []string) error {
        if timeout <= 0 {
            return fault.New("BAD_CONFIG", "invalid_flags", nil)
        }
        root, file, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        p, err := o.Store.Existing(root)
        if err != nil {
            return err
        }
        name, t, err := config.Resolve(target, p, file, cfg)
        if err != nil {
            return err
        }
        p, err = o.Store.Select(cmd.Context(), root, "")
        if err != nil {
            return err
        }
        dir := file.RemoteDir
        if dir == "" {
            dir = path.Join(t.RemoteBase, p.ID)
        }
        requestCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
        defer cancel()
        r, err := o.Checker.Check(requestCtx, t, dir, jsonMode || !o.Interactive)
        checked := checkResult{Target: name, ProjectID: p.ID, Report: r}
        result = checked
        if err != nil {
            return err
        }
        if verifyRunner {
            if o.Runner == nil {
                return fault.New("BUILD_REQUIRED", "runner_missing", nil)
            }
            verified, err := o.Runner.Verify(requestCtx, t, r.Arch, jsonMode || !o.Interactive)
            if err != nil {
                return err
            }
            checked.Runner = &verified
            result = checked
        }
        return nil
    }
    version := &cobra.Command{Use: "version", Short: l.Text("version_short"), Args: arguments(0, 0), RunE: func(_ *cobra.Command, _ []string) error {
        info, _ := debug.ReadBuildInfo()
        result = versionData(info)
        return nil
    }}
    help := &cobra.Command{Use: "help " + l.Text("command_arg"), Short: l.Text("help_short"), Args: arguments(0, 1), RunE: func(_ *cobra.Command, args []string) error {
        c, _, err := root.Find(args)
        if err != nil {
            return fault.New("BAD_CONFIG", "invalid_command", nil)
        }
        return c.Help()
    }}
    root.SetHelpCommand(help)
    root.AddCommand(add, use, check, version, newSyncCommand(o, cfg, &target, l, func(value any) { result = value }))
    root.AddCommand(newTargetCommands(o, cfg, l, func(value any) { result = value })...)
    root.AddCommand(newRunCommands(o, cfg, &target, l, func(value any) { result = value }, &streamed)...)
    root.AddCommand(newPullCommand(o, cfg, &target, l, func(value any) { result = value }))
    root.AddCommand(newMachineCommands(o, cfg, &target, l, func(value any) { result = value }, &streamed)...)
    root.AddCommand(newCleanCommand(o, &target, l, func(value any) { result = value }))
    err := earlyErr
    if err == nil {
        err = langErr
    }
    if err == nil {
        err = cfgErr
    }
    if err == nil {
        if _, _, findErr := root.Find(args); findErr != nil {
            f := fault.New("BAD_CONFIG", "invalid_command", nil)
            f.Hint = "help_hint"
            err = f
        } else {
            _, err = root.ExecuteContextC(ctx)
        }
    }
    var view *errorView
    code := 0
    if err != nil {
        var f *fault.Error
        if !errors.As(err, &f) {
            f = fault.Wrap("OPERATION_FAILED", "internal_error", err)
        }
        view = &errorView{Code: f.Code, Message: l.Text(f.Message, f.Params)}
        if f.Hint != "" {
            view.Hint = l.Text(f.Hint, f.Params)
        }
        if detail, ok := f.Params["Detail"].(string); ok {
            view.Detail = detail
        }
        code = 1
        if f.Code == "BAD_CONFIG" {
            code = 2
        }
        if f.Code == "SUBMISSION_UNKNOWN" || f.Code == "STOP_UNCONFIRMED" || f.Code == "EXEC_UNCONFIRMED" || f.Code == "CLEAN_UNCONFIRMED" {
            code = 3
        }
    }
    if jsonMode && (!streamed || view != nil) {
        if streamed && result == nil {
            result = map[string]string{"type": "error"}
        }
        if err := json.NewEncoder(o.Out).Encode(envelope{1, view == nil, result, view}); err != nil {
            return 1
        }
    } else if !jsonMode {
        if result != nil {
            printResult(o.Out, result, l, view == nil)
        }
        if view != nil {
            fmt.Fprintln(o.Err, view.Message)
            if view.Detail != "" {
                fmt.Fprintln(o.Err, view.Detail)
            }
            if view.Hint != "" {
                fmt.Fprintln(o.Err, view.Hint)
            }
        }
    }
    return code
}

func earlyOptions(args []string) (bool, string, error) {
    jsonMode := false
    lang := ""
    var parseErr error
    for i := 0; i < len(args); i++ {
        a := args[i]
        if a == "--" {
            break
        }
        if a == "--ssh" || a == "--remote-dir" || a == "--target" || a == "--timeout" || a == "--wait" || a == "--tail" || a == "--limit" || a == "--exclude" || a == "--mode" || a == "--grace" || a == "--path" || a == "--to" || a == "--status" || a == "--since" || a == "--until" || a == "--cwd" || a == "--local-port" {
            i++
            continue
        }
        if a == "--json" || a == "--json=true" {
            jsonMode = true
        }
        if a == "--json=false" {
            jsonMode = false
        }
        if strings.HasPrefix(a, "--lang=") {
            lang = strings.TrimPrefix(a, "--lang=")
            if lang == "" {
                parseErr = fault.New("BAD_CONFIG", "language_invalid", map[string]any{"Languages": strings.Join(i18n.Supported(), ", ")})
            }
        }
        if a == "--lang" {
            if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
                parseErr = fault.New("BAD_CONFIG", "invalid_flags", nil)
                continue
            }
            i++
            lang = args[i]
        }
    }
    return jsonMode, lang, parseErr
}

func arguments(min, max int) cobra.PositionalArgs {
    return func(cmd *cobra.Command, args []string) error {
        if len(args) < min || len(args) > max {
            f := fault.New("BAD_CONFIG", "argument_count", map[string]any{"Command": cmd.CommandPath()})
            f.Hint = "help_hint"
            return f
        }
        return nil
    }
}

func printHelp(out io.Writer, c *cobra.Command, l *i18n.Localizer) {
    use := c.CommandPath()
    if parts := strings.SplitN(c.Use, " ", 2); len(parts) == 2 {
        use += " " + parts[1]
    }
    usage := use + " " + l.Text("options_arg")
    if c.Name() == "exec" {
        usage = c.CommandPath() + " " + l.Text("options_arg") + " " + strings.SplitN(c.Use, " ", 2)[1]
    }
    fmt.Fprintf(out, "%s\n\n%s\n  %s\n", c.Short, l.Text("usage"), usage)
    if c.HasAvailableSubCommands() {
        fmt.Fprintf(out, "\n%s\n", l.Text("commands"))
        for _, sub := range c.Commands() {
            if !sub.Hidden {
                fmt.Fprintf(out, "  %-10s %s\n", sub.Name(), sub.Short)
            }
        }
    }
    fmt.Fprintf(out, "\n%s\n", l.Text("flags"))
    flags := pflag.NewFlagSet("help", pflag.ContinueOnError)
    flags.AddFlagSet(c.InheritedFlags())
    flags.AddFlagSet(c.LocalFlags())
    flags.VisitAll(func(f *pflag.Flag) {
        if f.Name == "target" && (c.Name() == "logs" || c.Name() == "stop" || c.Name() == "pull" || c.Name() == "targets" || c.Name() == "remove" || c.Name() == "clean") {
            return
        }
        name := "--" + f.Name
        if f.Shorthand != "" {
            name = "-" + f.Shorthand + ", " + name
        }
        if f.Value.Type() != "bool" {
            name += " <" + l.Text("value") + ">"
        }
        desc := f.Usage
        if f.Name == "json" && c.Name() == "ssh" {
            desc = l.Text("interactive_json")
        }
        if f.Name == "json" && c.Name() == "forward" {
            desc = l.Text("forward_json")
        }
        if f.DefValue != "" && f.DefValue != "false" {
            desc += " (" + l.Text("default_value", map[string]any{"Value": f.DefValue}) + ")"
        }
        fmt.Fprintf(out, "  %s %s\n", padFlag(name, 22), desc)
    })
}

// Flag labels contain ASCII option names and localized CJK value placeholders.
func padFlag(s string, cells int) string {
    n := 0
    for _, r := range s {
        n++
        kind := width.LookupRune(r).Kind()
        if kind == width.EastAsianWide || kind == width.EastAsianFullwidth {
            n++
        }
    }
    return s + strings.Repeat(" ", max(0, cells-n))
}

func printResult(out io.Writer, data any, l *i18n.Localizer, ok bool) {
    switch r := data.(type) {
    case addResult:
        fmt.Fprintln(out, l.Text("added", map[string]any{"Target": r.Target, "Destination": r.Destination}))
    case targetsResult:
        printTargets(out, r, l)
    case removeResult:
        fmt.Fprintln(out, l.Text("remove_done", map[string]any{"Target": r.Target}))
        if r.Selected {
            fmt.Fprintln(out, l.Text("remove_reselect"))
        }
    case useResult:
        fmt.Fprintln(out, l.Text("selected", map[string]any{"Root": r.Root, "Target": r.Target}))
    case syncResult:
        printSync(out, r, l)
    case sources.PullResult:
        printPull(out, r, l, ok)
    case cleanBatch:
        printClean(out, r, l)
    case execution.View:
        printRun(out, r, l)
    case runList:
        for _, run := range r.Runs {
            printRun(out, run, l)
        }
        if len(r.Runs) == 0 {
            if len(r.Filters.Statuses) > 0 || r.Filters.Target != "" || r.Filters.Since != nil || r.Filters.Until != nil {
                fmt.Fprintln(out, l.Text("ps_no_matches"))
            } else {
                fmt.Fprintln(out, l.Text("runs_empty"))
            }
        }
        if len(r.Filters.Statuses) > 0 || r.Filters.Target != "" || r.Filters.Since != nil || r.Filters.Until != nil || r.Truncated {
            fmt.Fprintln(out, l.Text("ps_scan_summary", map[string]any{"Count": r.Examined, "Total": r.Eligible, "Matched": r.Matched}))
        }
        if r.Unresolved > 0 {
            fmt.Fprintln(out, l.Text("ps_unresolved"))
        }
        if r.Truncated {
            fmt.Fprintln(out, l.Text("runs_truncated"))
        }
    case checkResult:
        fmt.Fprintln(out, l.Text("check_heading", map[string]any{"Target": r.Target}))
        if r.Report.OS == "" {
            return
        }
        fmt.Fprintf(out, "%s: %s / %s\n%s: %s\n", l.Text("platform"), r.Report.OS, r.Report.Arch, l.Text("remote_directory"), r.Report.RemoteDir)
        w := "no"
        if r.Report.Writable {
            w = "yes"
        }
        fmt.Fprintf(out, "%s: %s\n%s: %.2f GiB\n", l.Text("writable"), l.Text(w), l.Text("disk"), float64(r.Report.DiskAvailableBytes)/(1<<30))
        for _, group := range []struct {
            id    string
            tools map[string]ssh.Tool
        }{{"local_tools", r.Report.Local}, {"remote_tools", r.Report.Remote}} {
            fmt.Fprintln(out, l.Text(group.id)+":")
            for _, name := range []string{"ssh", "rsync", "python", "uv", "nvidia-smi", "nvcc"} {
                tool, exists := group.tools[name]
                if !exists {
                    continue
                }
                value := tool.Path
                if value == "" {
                    value = l.Text("missing")
                }
                fmt.Fprintf(out, "  %s: %s\n", name, value)
                if tool.Version != "" {
                    fmt.Fprintf(out, "    %s\n", strings.ReplaceAll(tool.Version, "\n", "\n    "))
                }
            }
        }
        if r.Report.GPU != "" {
            fmt.Fprintf(out, "%s: %s\n", l.Text("gpu"), r.Report.GPU)
        }
        if r.Runner != nil {
            fmt.Fprintln(out, l.Text("runner_verified", map[string]any{"Version": r.Runner.Version, "Path": r.Runner.Path}))
        }
        for _, issue := range r.Report.Issues {
            fmt.Fprintln(out, "- "+l.Text(issue))
        }
        if ok {
            fmt.Fprintln(out, l.Text("check_ok"))
        }
    default:
        if r, ok := data.(map[string]any); ok {
            fmt.Fprintf(out, "loaf %v (%v/%v)\n", r["version"], r["os"], r["arch"])
        }
    }
}
