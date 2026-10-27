package cli

import (
    "fmt"
    "io"
    "strconv"
    "strings"

    "github.com/alecthomas/kong"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "golang.org/x/text/width"
)

type parsedCommand struct {
    grammar *grammar
    node    *kong.Node
    path    string
    help    bool
}
type bootstrapOptions struct {
    json        bool
    language    string
    languageSet bool
    help        bool
}

func newParser(g *grammar) (*kong.Kong, error) {
    return kong.New(g, kong.Name("loaf"), kong.NoDefaultHelp(), kong.WithHyphenPrefixedParameters(true), kong.Writers(io.Discard, io.Discard))
}

// bootstrap reads only output/locale options using the compiled schema's arity.
// It never executes hooks or validates application configuration.
func bootstrap(model *kong.Application, args []string) bootstrapOptions {
    result := bootstrapOptions{}
    node := model.Node
    for i := 0; i < len(args); i++ {
        token := args[i]
        if token == "--" {
            break
        }
        if !strings.HasPrefix(token, "-") {
            for _, child := range node.Children {
                if child.Name == token {
                    node = child
                    break
                }
            }
            continue
        }
        key, inline, hasInline := strings.Cut(token, "=")
        flag := findFlag(node, key)
        if flag == nil {
            continue
        }
        raw := "true"
        if hasInline {
            raw = inline
        } else if !flag.IsBool() {
            if i+1 >= len(args) {
                continue
            }
            i++
            raw = args[i]
        }
        switch flag.Name {
        case "json":
            if v, err := strconv.ParseBool(raw); err == nil {
                result.json = v
            }
        case "lang":
            result.language = raw
            result.languageSet = true
        case "help":
            if v, err := strconv.ParseBool(raw); err == nil {
                result.help = v
            }
        }
    }
    return result
}

func findFlag(node *kong.Node, key string) *kong.Flag {
    for current := node; current != nil; current = current.Parent {
        for _, flag := range current.Flags {
            if key == "--"+flag.Name || (flag.Short != 0 && key == "-"+string(flag.Short)) {
                return flag
            }
        }
    }
    return nil
}

func parse(parser *kong.Kong, g *grammar, args []string, early bootstrapOptions) (parsedCommand, error) {
    traced, err := kong.Trace(parser, args)
    if err != nil {
        return parsedCommand{}, parseFailure()
    }
    node := parser.Model.Node
    for _, item := range traced.Path {
        if item.Command != nil {
            node = item.Command
        }
    }
    // Help is an output request; missing ordinary command arguments do not block it.
    if early.help && traced.Error == nil {
        return parsedCommand{g, node, node.Path(), true}, nil
    }
    if traced.Error == nil && (node == parser.Model.Node || node.Path() == "cloud") {
        return parsedCommand{g, node, node.Path(), true}, nil
    }
    context, err := parser.Parse(args)
    if err != nil {
        return parsedCommand{}, parseFailure()
    }
    node = parser.Model.Node
    for _, item := range context.Path {
        if item.Command != nil {
            node = item.Command
        }
    }
    path := node.Path()
    if path == "help" {
        node = parser.Model.Node
        for _, name := range g.Help.Command {
            var next *kong.Node
            for _, child := range node.Children {
                if child.Name == name {
                    next = child
                    break
                }
            }
            if next == nil {
                return parsedCommand{}, fault.New("BAD_CONFIG", "invalid_command", nil)
            }
            node = next
        }
        return parsedCommand{g, node, node.Path(), true}, nil
    }
    return parsedCommand{g, node, path, path == "" || path == "cloud"}, nil
}

func parseFailure() error {
    f := fault.New("BAD_CONFIG", "invalid_flags", nil)
    f.Hint = "help_hint"
    return f
}

func printHelp(out io.Writer, node *kong.Node, l *i18n.Localizer) {
    short := node.Help
    if short == "" {
        short = "root_short"
    }
    fmt.Fprintf(out, "%s\n\n%s\n  %s", l.Text(short), l.Text("usage"), node.FullPath())
    name := node.Name
    if name == "run" || name == "exec" {
        fmt.Fprintf(out, " %s %s", l.Text("options_arg"), l.Text(map[string]string{"run": "run_args", "exec": "exec_args"}[name]))
    } else {
        for _, arg := range node.Positional {
            if arg.Help != "" {
                fmt.Fprintf(out, " %s", l.Text(arg.Help))
            } else if arg.Required {
                fmt.Fprintf(out, " <%s>", arg.Name)
            } else {
                fmt.Fprintf(out, " [%s]", arg.Name)
            }
        }
        fmt.Fprintf(out, " %s", l.Text("options_arg"))
    }
    fmt.Fprintln(out)
    if len(node.Children) > 0 {
        fmt.Fprintln(out, "\n"+l.Text("commands"))
        for _, child := range node.Children {
            if !child.Hidden {
                fmt.Fprintf(out, "  %-12s %s\n", child.Name, l.Text(child.Help))
            }
        }
    }
    fmt.Fprintln(out, "\n"+l.Text("flags"))
    for _, group := range node.AllFlags(false) {
        for _, flag := range group {
            if flag.Hidden {
                continue
            }
            if flag.Name == "target" && targetForbidden(node.Path()) {
                continue
            }
            label := "--" + flag.Name
            if flag.Short != 0 {
                label = "-" + string(flag.Short) + ", " + label
            }
            if !flag.IsBool() {
                label += " <" + l.Text("value") + ">"
            }
            message := flag.Help
            if flag.Name == "target" && name == "ps" {
                message = "ps_flag_target"
            }
            if flag.Name == "json" && name == "ssh" {
                message = "interactive_json"
            }
            if flag.Name == "json" && name == "forward" {
                message = "forward_json"
            }
            description := l.Text(message)
            if flag.HasDefault && flag.Default != "" && flag.Default != "false" {
                description += " (" + l.Text("default_value", map[string]any{"Value": flag.Default}) + ")"
            }
            fmt.Fprintf(out, "  %s %s\n", padFlag(label, 22), description)
        }
    }
}

func targetForbidden(path string) bool {
    if path == "cloud" || strings.HasPrefix(path, "cloud ") {
        return true
    }
    switch path {
    case "logs", "stop", "pull", "targets", "remove", "clean", "inspect", "outputs", "wait":
        return true
    }
    return false
}

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
