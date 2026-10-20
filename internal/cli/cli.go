// Package cli owns argument parsing and localized or structured presentation.
package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "runtime"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/wire"
    "github.com/spf13/cobra"
    "github.com/spf13/pflag"
    "golang.org/x/text/width"
)

// Options are assembled by the executable, not looked up inside command handlers.
type Options struct {
    Store       *config.Store
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
    root := &cobra.Command{Use: "loaf", Short: l.Text("root_short"), SilenceErrors: true, SilenceUsage: true, DisableAutoGenTag: true}
    root.CompletionOptions.DisableDefaultCmd = true
    root.SetIn(o.In)
    root.SetOut(o.Out)
    root.SetErr(o.Err)
    root.SetArgs(args)
    root.PersistentFlags().String("lang", "auto", l.Text("flag_lang"))
    root.PersistentFlags().Bool("json", false, l.Text("flag_json"))
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
    version := &cobra.Command{Use: "version", Short: l.Text("version_short"), Args: arguments(0, 0), RunE: func(_ *cobra.Command, _ []string) error {
        result = map[string]any{"version": wire.Version, "format_version": wire.FormatVersion, "os": runtime.GOOS, "arch": runtime.GOARCH}
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
    root.AddCommand(version)
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
        if f.Code == "SUBMISSION_UNKNOWN" || f.Code == "STOP_UNCONFIRMED" {
            code = 3
        }
    }
    if jsonMode {
        if err := json.NewEncoder(o.Out).Encode(envelope{1, view == nil, result, view}); err != nil {
            return 1
        }
    } else {
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
    fmt.Fprintf(out, "%s\n\n%s\n  %s %s\n", c.Short, l.Text("usage"), use, l.Text("options_arg"))
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
        name := "--" + f.Name
        if f.Shorthand != "" {
            name = "-" + f.Shorthand + ", " + name
        }
        if f.Value.Type() != "bool" {
            name += " <" + l.Text("value") + ">"
        }
        desc := f.Usage
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

func printResult(out io.Writer, data any, _ *i18n.Localizer, _ bool) {
    if r, ok := data.(map[string]any); ok {
        fmt.Fprintf(out, "loaf %v (%v/%v)\n", r["version"], r["os"], r["arch"])
    }
}
