// Package cli owns argument parsing and localized or structured presentation.
package cli

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "runtime/debug"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
)

// Options are assembled by the executable, not looked up inside command handlers.
type Options struct {
    App                                        *client.App
    Language                                   string
    InitError                                  error
    ReadPassword                               func(context.Context) ([]byte, error)
    Env                                        map[string]string
    In                                         io.Reader
    Out, Err                                   io.Writer
    TerminalOutput, TerminalError, Interactive bool
    Trace                                      *ssh.Trace
}

type errorView struct {
    Reason          string          `json:"reason,omitempty"`
    Argument        string          `json:"argument,omitempty"`
    HelpCommand     []string        `json:"help_command,omitempty"`
    Location        *fault.Location `json:"location,omitempty"`
    FailureKind     string          `json:"failure_kind,omitempty"`
    Code            string          `json:"code"`
    Message         string          `json:"message"`
    Hint            string          `json:"hint,omitempty"`
    Detail          string          `json:"detail,omitempty"`
    ProviderCode    string          `json:"provider_code,omitempty"`
    AuthStage       string          `json:"auth_stage,omitempty"`
    HTTPStatus      int             `json:"http_status,omitempty"`
    Stage           string          `json:"stage,omitempty"`
    ExpectedVersion string          `json:"expected_version,omitempty"`
    ActualVersion   string          `json:"actual_version,omitempty"`
}
type envelope struct {
    SchemaVersion int              `json:"schema_version"`
    OK            bool             `json:"ok"`
    Data          any              `json:"data,omitempty"`
    Error         *errorView       `json:"error,omitempty"`
    Trace         *ssh.TraceReport `json:"trace,omitempty"`
}

// Execute parses one invocation and returns its public operation exit code.
func Execute(ctx context.Context, args []string, o Options) int {
    if o.Trace == nil {
        o.Trace = &ssh.Trace{}
    }
    o.Trace.Reset()
    model := &grammar{}
    parser, err := newParser(model)
    if err != nil {
        fmt.Fprintln(o.Err, err)
        return 1
    }
    early := bootstrap(parser.Model, args)
    preferred := early.language
    if !early.languageSet {
        preferred = o.Env["LOAF_LANG"]
        if preferred == "" {
            if o.InitError == nil {
                preferred = o.Language
            }
        }
        if preferred == "" {
            preferred = "auto"
        }
    }
    l, langErr := i18n.New(preferred, o.Env)
    if langErr != nil && !early.languageSet && o.Env["LOAF_LANG"] == "" && (early.help || early.version || len(args) > 0 && (args[0] == "help" || args[0] == "version")) {
        l, langErr = i18n.New("auto", o.Env)
    }
    p := &presenter{json: early.json, out: o.Out, err: o.Err, locale: l, trace: o.Trace}
    if early.languageSet && preferred == "" {
        langErr = fault.New("BAD_CONFIG", "language_invalid", map[string]any{"Languages": strings.Join(i18n.Supported(), ", ")})
    }
    if langErr != nil {
        var failure *fault.Error
        if errors.As(langErr, &failure) {
            argument := "--lang"
            if !early.languageSet && o.Env["LOAF_LANG"] != "" {
                argument = "LOAF_LANG"
            }
            failure.Input = &fault.Input{Reason: "invalid_value", Argument: argument, HelpCommand: []string{"loaf", "help"}}
        }
        return p.finish(nil, langErr)
    }
    parsed, err := parse(parser, model, args, early)
    if err != nil {
        return p.finish(nil, err)
    }
    if parsed.help {
        help := helpData(parsed.node, l)
        if p.json {
            return p.finish(help, nil)
        }
        if _, err := io.WriteString(o.Out, help.Help); err != nil {
            return 1
        }
        return 0
    }
    p.command = parsed.path
    if parsed.path == "version" {
        info, _ := debug.ReadBuildInfo()
        return p.finish(versionData(info), nil)
    }
    if o.InitError != nil {
        return p.finish(nil, o.InitError)
    }
    if model.Target != nil && targetForbidden(parsed.path) {
        return p.finish(nil, inputFailure("conflicting_arguments", "--target", "input_target_forbidden", nil, parsed.node))
    }
    if model.Trace {
        if model.Logs.Follow || parsed.path == "exec" || parsed.path == "ssh" || parsed.path == "forward" || parsed.path == "cloud watch" {
            return p.finish(nil, fault.New("BAD_CONFIG", "trace_finite_only", nil))
        }
        o.Trace.Enable()
    }
    input := interaction.New(interaction.Options{In: o.In, Err: o.Err, Interactive: o.Interactive && !p.json, Rich: o.TerminalError && o.Env["TERM"] != "dumb", ReadPassword: o.ReadPassword})
    call := invocation{app: o.App, options: o, grammar: model, output: p, input: input, policy: client.CallPolicy{Batch: p.json || !o.Interactive}}
    result, err := call.dispatch(ctx, parsed.path)
    if p.json && (parsed.path == "exec" || parsed.path == "cloud watch" || parsed.path == "logs" && model.Logs.Follow) {
        return p.finishStream(result, err)
    }
    return p.finish(result, err)
}

type presenter struct {
    waited   bool
    command  string
    json     bool
    out, err io.Writer
    locale   *i18n.Localizer
    trace    *ssh.Trace
    writeErr error
}

func (p *presenter) stream(data any) error {
    if p.writeErr == nil {
        p.writeErr = json.NewEncoder(p.out).Encode(envelope{SchemaVersion: 1, OK: true, Data: data})
    }
    return p.writeErr
}
func (p *presenter) error(err error) *errorView {
    if err == nil {
        return nil
    }
    var f *fault.Error
    if !errors.As(err, &f) {
        if errors.Is(err, interaction.ErrCanceled) || errors.Is(err, context.Canceled) {
            f = fault.Wrap("OPERATION_CANCELED", "operation_canceled", err)
        } else {
            f = fault.Wrap("OPERATION_FAILED", "internal_error", err)
        }
    }
    parameters := f.Params
    if f.Message == "input_invalid_type" {
        parameters = map[string]any{}
        for k, v := range f.Params {
            parameters[k] = v
        }
        if kind, ok := parameters["Value"].(string); ok {
            parameters["Value"] = p.locale.Text("type_" + kind)
        }
    }
    view := &errorView{FailureKind: ssh.FailureKind(err), Code: f.Code, Message: p.locale.Text(f.Message, parameters), Stage: fault.Stage(err)}
    if f.Input != nil {
        view.Reason, view.Argument = f.Input.Reason, f.Input.Argument
        view.Location, view.HelpCommand = f.Input.Location, f.Input.HelpCommand
        if len(view.HelpCommand) == 0 {
            view.HelpCommand = append([]string{"loaf", "help"}, strings.Fields(p.command)...)
        }
    }
    view.ProviderCode, _ = f.Params["ProviderCode"].(string)
    view.HTTPStatus, _ = f.Params["HTTPStatus"].(int)
    view.AuthStage, _ = f.Params["AuthStage"].(string)
    if f.Code == "VERSION_MISMATCH" {
        view.ExpectedVersion, _ = f.Params["Expected"].(string)
        view.ActualVersion, _ = f.Params["Actual"].(string)
    }
    if f.Hint != "" {
        parameters := f.Params
        if view.AuthStage != "" {
            parameters = map[string]any{}
            for key, value := range f.Params {
                parameters[key] = value
            }
            parameters["Stage"] = p.locale.Text("cloud_auth_stage_" + view.AuthStage)
            if view.HTTPStatus == 0 {
                parameters["HTTPStatus"] = p.locale.Text("cloud_http_status_unavailable")
            }
        }
        view.Hint = p.locale.Text(f.Hint, parameters)
    }
    view.Detail, _ = f.Params["Detail"].(string)
    return view
}
func exitCode(view *errorView) int {
    code := 0
    if view != nil {
        code = 1
        if view.Code == "BAD_CONFIG" {
            code = 2
        }
        switch view.Code {
        case "SUBMISSION_UNKNOWN", "STOP_UNCONFIRMED", "EXEC_UNCONFIRMED", "CLEAN_UNCONFIRMED", "CLOUD_UNCONFIRMED":
            code = 3
        }
    }
    return code
}

// finishStream emits an explicit final event, or only an error if the producer
// already emitted its end event. A failed consumer is never written to again.
func (p *presenter) finishStream(data any, err error) int {
    if p.writeErr != nil {
        return 1
    }
    if data == nil && err == nil {
        return 0
    }
    if data == nil {
        data = struct {
            Type string `json:"type"`
        }{Type: "error"}
    }
    view := p.error(err)
    if err := json.NewEncoder(p.out).Encode(envelope{SchemaVersion: 1, OK: view == nil, Data: data, Error: view}); err != nil {
        return 1
    }
    return exitCode(view)
}

// finish presents one finite result; native streams have no result to repeat.
func (p *presenter) finish(data any, err error) int {
    view := p.error(err)
    if p.json {
        if err := json.NewEncoder(p.out).Encode(envelope{SchemaVersion: 1, OK: view == nil, Data: data, Error: view, Trace: p.trace.Snapshot()}); err != nil {
            return 1
        }
        return exitCode(view)
    }
    if trace := p.trace.Snapshot(); trace != nil {
        printTrace(p.err, *trace, p.locale)
    }
    if data != nil {
        if renderErr := printResult(p.out, data, p.locale, view == nil); renderErr != nil && view == nil {
            view = p.error(renderErr)
        }
        if run, ok := data.(execution.View); ok && p.command == "run" && run.ID != "" {
            printRunNext(p.out, run, p.locale, p.waited && err == nil)
        }
    }
    if view != nil {
        fmt.Fprintln(p.err, view.Message)
        if view.Location != nil {
            location := view.Location.File
            if view.Location.Line > 0 {
                location += fmt.Sprintf(":%d:%d", view.Location.Line, view.Location.Column)
            }
            if view.Location.Field != "" {
                location += " (" + view.Location.Field + ")"
            }
            fmt.Fprintln(p.err, p.locale.Text("input_location", map[string]any{"Path": location}))
        }
        if view.Stage != "" {
            fmt.Fprintln(p.err, p.locale.Text("operation_stage", map[string]any{"Stage": p.locale.Text("operation_stage_" + view.Stage)}))
        }
        if view.Detail != "" {
            fmt.Fprintln(p.err, view.Detail)
        }
        if view.Hint != "" {
            fmt.Fprintln(p.err, view.Hint)
        } else if len(view.HelpCommand) > 0 {
            fmt.Fprintln(p.err, p.locale.Text("input_help_hint", map[string]any{"Command": strings.Join(view.HelpCommand, " ")}))
        }
    }
    return exitCode(view)
}

type addResult = client.AddResult
type useResult = client.UseResult
type targetEntry = client.TargetEntry
type choice = client.TargetChoice
type targetsResult = client.TargetsResult
type removeResult = client.RemoveResult
type syncResult = client.SyncResult
type cleanBatch = client.CleanBatch
type runList = execution.History
type cloudAccounts = client.CloudAccounts
type cloudReceipts = client.CloudReceipts
type cloudBinding = client.CloudBinding
type cloudAcquisition = client.CloudAcquisition
type cloudCredentials = client.CloudCredentials
type loginResult = client.LoginResult
type logoutResult = client.LogoutResult

type checkResult struct {
    Target        string                `json:"target"`
    ProjectID     string                `json:"project_id"`
    Report        ssh.Report            `json:"report"`
    Runner        *execution.RunnerInfo `json:"runner,omitempty"`
    Steps         map[string]string     `json:"steps,omitempty"`
    ElapsedMillis int64                 `json:"elapsed_millis"`
    Error         *errorView            `json:"error,omitempty"`
}
type checkBatch struct {
    Checks     []checkResult `json:"checks"`
    Selected   int           `json:"selected"`
    Completed  int           `json:"completed"`
    Failed     int           `json:"failed"`
    Unexamined int           `json:"unexamined"`
}
type pullRow struct {
    Result sources.PullResult `json:"result"`
    Error  *errorView         `json:"error,omitempty"`
}
type pullBatch struct {
    Runs       []pullRow `json:"runs"`
    Selected   int       `json:"selected"`
    Completed  int       `json:"completed"`
    Failed     int       `json:"failed"`
    Unexamined int       `json:"unexamined"`
}

func (p *presenter) check(r client.CheckResult) checkResult {
    return checkResult{r.Target, r.ProjectID, r.Report, r.Runner, r.Steps, r.ElapsedMillis, p.error(r.Error)}
}
func (p *presenter) checks(r client.CheckBatch) checkBatch {
    out := checkBatch{Checks: []checkResult{}, Selected: r.Selected, Completed: r.Completed, Failed: r.Failed, Unexamined: r.Unexamined}
    for _, row := range r.Checks {
        out.Checks = append(out.Checks, p.check(row))
    }
    return out
}
func (p *presenter) pulls(r client.PullBatch) pullBatch {
    out := pullBatch{Runs: []pullRow{}, Selected: r.Selected, Completed: r.Completed, Failed: r.Failed, Unexamined: r.Unexamined}
    for _, row := range r.Runs {
        out.Runs = append(out.Runs, pullRow{row.Result, p.error(row.Error)})
    }
    return out
}
