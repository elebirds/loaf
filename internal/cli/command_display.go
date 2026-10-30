package cli

import (
    "fmt"
    "strings"
    "unicode"
    "unicode/utf8"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "mvdan.cc/sh/v3/syntax"
)

type nextAction struct {
    Kind                 string   `json:"kind"`
    Command              []string `json:"command"`
    RequiresConfirmation bool     `json:"requires_confirmation"`
}

// uxJCommandDisplay quotes public argv for a POSIX shell without expansion.
// Unrepresentable/control bytes fail rather than appearing as terminal controls
// or being silently replaced. The returned error never contains the input.
func uxJCommandDisplay(argv []string) (string, error) {
    words := make([]string, len(argv))
    for i, arg := range argv {
        if !utf8.ValidString(arg) || strings.ContainsFunc(arg, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
            return "", uxJCommandError()
        }
        word, err := syntax.Quote(arg, syntax.LangPOSIX)
        if err != nil {
            return "", uxJCommandError()
        }
        // POSIX has no history expansion, but copied commands also commonly
        // enter interactive Bash/zsh. Keep their exclamation marks literal.
        if strings.ContainsRune(arg, '!') {
            word = ssh.Quote(arg)
        }
        words[i] = word
    }
    return strings.Join(words, " "), nil
}

// uxJReadCommand uses a new compiled grammar, without configuration, credentials,
// application services or execution. Domain owners still validate effective
// values, identity, applicability and public provenance before suggesting them.
func uxJReadCommand(argv []string) (parsedCommand, error) {
    if len(argv) < 2 || argv[0] != "loaf" {
        return parsedCommand{}, uxJCommandError()
    }
    if _, err := uxJCommandDisplay(argv); err != nil {
        return parsedCommand{}, err
    }
    model := &grammar{}
    parser, err := newParser(model)
    if err != nil {
        return parsedCommand{}, uxJCommandError()
    }
    parsed, err := parse(parser, model, argv[1:], bootstrap(parser.Model, argv[1:]))
    if err != nil {
        // Parse diagnostics may contain the unknown token. This helper accepts
        // generated commands, so only report the broken command construction.
        return parsedCommand{}, uxJCommandError()
    }
    if parsed.help || parsed.path == "version" {
        return parsed, nil
    }
    if model.Target != nil && targetForbidden(parsed.path) {
        return parsedCommand{}, uxJCommandError()
    }
    var args []string
    switch parsed.path {
    case "run":
        args = model.Run.Argv
    case "exec":
        args = model.Exec.Argv
    case "queue submit":
        args = model.Queue.Submit.Argv
    }
    if _, err := commandArgs(args, parsed.path == "exec"); err != nil {
        return parsedCommand{}, uxJCommandError()
    }
    if model.JSON && (parsed.path == "ssh" || parsed.path == "forward" || parsed.path == "notify channel") || model.Trace && parsed.path == "notify channel" {
        return parsedCommand{}, uxJCommandError()
    }
    return parsed, nil
}

// uxJNextAction copies an already selected domain action into the existing JSON
// contract. Callers supply public inputs, never historical private execution
// specs, secrets or guessed values. Parsing cannot establish those properties.
// In particular, requiresConfirmation is evidence to display, not approval.
func uxJNextAction(kind string, argv []string, requiresConfirmation bool) (nextAction, error) {
    if kind == "" || strings.ContainsFunc(kind, func(r rune) bool { return r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') }) {
        return nextAction{}, uxJCommandError()
    }
    parsed, err := uxJReadCommand(argv)
    if err != nil {
        return nextAction{}, err
    }
    for _, group := range parsed.node.AllFlags(false) {
        for _, flag := range group {
            if flag.Set && (flag.Name == "yes" || flag.Name == "show-password") {
                return nextAction{}, uxJCommandError()
            }
        }
    }
    return nextAction{Kind: kind, Command: append([]string(nil), argv...), RequiresConfirmation: requiresConfirmation}, nil
}

func uxJCommandError() error {
    return fault.Invalid("command", "invalid_value", "invalid_flags")
}

func (p *presenter) printNextActions(data any) error {
    binding, bound := data.(client.CloudBinding)
    for _, a := range p.nextActions {
        if bound && binding.Status == "ready" && a.Kind == "select_machine" {
            continue
        }
        display, err := uxJCommandDisplay(a.Command)
        if err != nil {
            return err
        }
        message := "cloud_next_action"
        if p.command == "run" || p.command == "status" {
            message = "run_next_" + a.Kind
        }
        if _, err := fmt.Fprintln(p.out, p.locale.Text(message, map[string]any{"Value": display})); err != nil {
            return err
        }
    }
    return nil
}
