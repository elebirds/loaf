package cli

import (
    "errors"
    "reflect"
    "strconv"
    "strings"
    "unicode"

    "github.com/alecthomas/kong"
    "github.com/elebirds/loaf/internal/fault"
)

func helpInvocation(node *kong.Node) []string {
    args := []string{"loaf", "help"}
    if node != nil && node.Path() != "help" {
        args = append(args, strings.Fields(node.Path())...)
    }
    return args
}

func inputFailure(reason, argument, message string, params map[string]any, node *kong.Node) *fault.Error {
    owned := make(map[string]any, len(params)+2)
    for key, value := range params {
        owned[key] = value
    }
    owned["Argument"] = argument
    owned["Command"] = strings.Join(helpInvocation(node), " ")
    f := fault.New("BAD_CONFIG", message, owned)
    f.Hint = "input_help_hint"
    f.Input = &fault.Input{Reason: reason, Argument: argument, HelpCommand: helpInvocation(node)}
    return f
}

// diagnoseParse uses Kong's consumed path and remaining tokens. It never
// exposes a decoder's raw error, which can contain secret argument values.
func diagnoseParse(ctx *kong.Context) error {
    node := tracedNode(ctx)
    remainder := ctx.Path[len(ctx.Path)-1].Remainder()
    if len(remainder) > 0 {
        token := remainder[0]
        key, raw, inline := strings.Cut(token, "=")
        if strings.HasPrefix(key, "-") && key != "--" {
            flag := findFlag(node, key)
            if flag == nil {
                return inputFailure("unknown_option", safeToken(key), "input_unknown_option", nil, node)
            }
            if !inline && len(remainder) > 1 {
                raw = remainder[1]
            }
            reason, message := typeDiagnostic(flag.Value, raw, ctx.Error)
            if !flag.IsBool() && !inline && len(remainder) == 1 {
                reason, message = "missing_argument", "input_missing_argument"
            }
            return inputFailure(reason, "--"+flag.Name, message, map[string]any{"Value": valueType(flag.Value)}, node)
        }
        if len(node.Children) > 0 {
            return commandDiagnostic(node, token)
        }
        for _, arg := range node.Positional {
            if arg.Active && !arg.Set {
                reason, message := typeDiagnostic(arg, token, ctx.Error)
                return inputFailure(reason, arg.Name, message, map[string]any{"Value": valueType(arg)}, node)
            }
        }
        return inputFailure("invalid_value", "", "input_extra_arguments", nil, node)
    }
    for _, arg := range node.Positional {
        if arg.Required && !arg.Set {
            return inputFailure("missing_argument", arg.Name, "input_missing_argument", nil, node)
        }
    }
    for _, group := range node.AllFlags(false) {
        for _, flag := range group {
            if flag.Required && !flag.Set {
                return inputFailure("missing_argument", "--"+flag.Name, "input_missing_argument", nil, node)
            }
        }
    }
    return inputFailure("invalid_value", "", "invalid_flags", nil, node)
}

// Kong does not retain strconv errors for integers. Re-check only the failing
// value's declared numeric type to distinguish overflow without parsing English.
func typeDiagnostic(value *kong.Value, raw string, cause error) (string, string) {
    var number *strconv.NumError
    if errors.As(cause, &number) && errors.Is(number.Err, strconv.ErrRange) {
        return "out_of_range", "input_out_of_range"
    }
    if valueType(value) == "int" {
        t := value.Target.Type()
        for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
            t = t.Elem()
        }
        if _, err := strconv.ParseInt(raw, 0, t.Bits()); errors.Is(err, strconv.ErrRange) {
            return "out_of_range", "input_out_of_range"
        }
    }
    return "invalid_type", "input_invalid_type"
}

func commandDiagnostic(node *kong.Node, command string) error {
    if node.Parent == nil {
        replacements := map[string]string{"add": "machine add", "use": "machine use", "targets": "machine list / machine show NAME", "remove": "machine remove", "ps": "status"}
        if next, ok := replacements[command]; ok {
            return inputFailure("command_replaced", command, "input_command_replaced", map[string]any{"Value": "loaf " + next}, node)
        }
    }
    return inputFailure("unknown_command", safeToken(command), "input_unknown_command", nil, node)
}

func safeToken(value string) string {
    out := []rune{}
    for _, r := range value {
        if len(out) == 80 {
            break
        }
        if !unicode.IsControl(r) {
            out = append(out, r)
        }
    }
    return string(out)
}
