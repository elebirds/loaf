package cli

import (
    "errors"
    "io"
    "strconv"
    "strings"

    "github.com/alecthomas/kong"
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
    version     bool
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
        case "version":
            if v, err := strconv.ParseBool(raw); err == nil {
                result.version = v
            }
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
    node := tracedNode(traced)
    if traced.Error != nil {
        return parsedCommand{}, diagnoseParse(traced)
    }
    if early.version {
        return parsedCommand{g, parser.Model.Node, "version", false}, nil
    }
    if early.help {
        return parsedCommand{g, node, node.Path(), true}, nil
    }
    if node == parser.Model.Node || node.Path() == "cloud" {
        return parsedCommand{g, node, node.Path(), true}, nil
    }
    context, err := parser.Parse(args)
    if err != nil {
        var pe *kong.ParseError
        if errors.As(err, &pe) && pe.Context != nil {
            return parsedCommand{}, diagnoseParse(pe.Context)
        }
        return parsedCommand{}, diagnoseParse(traced)
    }
    node = tracedNode(context)
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
                return parsedCommand{}, commandDiagnostic(node, name)
            }
            node = next
        }
        return parsedCommand{g, node, node.Path(), true}, nil
    }
    return parsedCommand{g, node, path, path == "" || path == "cloud"}, nil
}

func tracedNode(ctx *kong.Context) *kong.Node {
    node := ctx.Model.Node
    for _, item := range ctx.Path {
        if item.Command != nil {
            node = item.Command
        }
    }
    return node
}

func parseFailure() error { return inputFailure("invalid_value", "", "invalid_flags", nil, nil) }

func targetForbidden(path string) bool {
    if path == "cloud" || strings.HasPrefix(path, "cloud ") || path == "machine" || strings.HasPrefix(path, "machine ") {
        return true
    }
    switch path {
    case "logs", "stop", "pull", "clean", "inspect", "outputs", "wait":
        return true
    }
    return false
}
