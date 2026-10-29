package cli

import (
    "fmt"
    "io"
    "reflect"
    "strconv"
    "strings"
    "time"

    "github.com/alecthomas/kong"
    "github.com/elebirds/loaf/internal/i18n"
    "golang.org/x/text/width"
)

type helpValue struct {
    Group          string   `json:"group,omitempty"`
    Requires       []string `json:"requires,omitempty"`
    ConflictsWith  []string `json:"conflicts_with,omitempty"`
    ExactlyOneOf   []string `json:"exactly_one_of,omitempty"`
    ApplicableWhen []string `json:"applicable_when,omitempty"`
    Enum           []string `json:"enum,omitempty"`
    Name           string   `json:"name"`
    Type           string   `json:"type"`
    Description    string   `json:"description"`
    Required       bool     `json:"required"`
    Variadic       bool     `json:"variadic,omitempty"`
    Repeatable     bool     `json:"repeatable"`
    ListFormat     string   `json:"list_format,omitempty"`
    Default        any      `json:"default,omitempty"`
    Configurable   bool     `json:"configurable"`
    ConfigKey      string   `json:"config_key,omitempty"`
}
type helpChild struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}
type helpResult struct {
    Help      string      `json:"help"`
    Command   string      `json:"command"`
    Arguments []helpValue `json:"arguments"`
    Flags     []helpValue `json:"flags"`
    Examples  [][]string  `json:"examples"`
    Commands  []helpChild `json:"commands"`
}

func helpText(id string, l *i18n.Localizer) string {
    if id == "" {
        return ""
    }
    return l.Text(id)
}

func valueType(v *kong.Value) string {
    if t := v.Tag.Get("value-type"); t != "" {
        return t
    }
    t := v.Target.Type()
    for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
        t = t.Elem()
    }
    if t == reflect.TypeFor[time.Duration]() {
        return "duration"
    }
    if v.IsBool() {
        return "bool"
    }
    switch t.Kind() {
    case reflect.Int, reflect.Int64:
        return "int"
    }
    return "string"
}

func describeValue(v *kong.Value, l *i18n.Localizer) helpValue {
    kind := valueType(v)
    h := helpValue{Name: v.Name, Type: kind, Description: helpText(v.Help, l), Required: v.Required, Repeatable: v.IsCumulative(), ConfigKey: v.Tag.Get("config")}
    h.Group = v.Tag.Get("ux-group")
    h.Configurable = h.ConfigKey != ""
    h.Requires = strings.Fields(v.Tag.Get("requires"))
    h.ConflictsWith = strings.Fields(v.Tag.Get("conflicts-with"))
    h.ExactlyOneOf = strings.Fields(v.Tag.Get("exactly-one-of"))
    h.ApplicableWhen = strings.Fields(v.Tag.Get("applicable-when"))
    if values := v.Tag.Get("enum"); values != "" {
        h.Enum = strings.Split(values, ",")
    }
    if kind == "csv" {
        h.ListFormat = "csv"
        h.Repeatable = true
    } else if h.Repeatable {
        h.ListFormat = "repeat"
    }
    if v.Flag == nil {
        h.Variadic = v.IsCumulative()
    }
    raw, present := v.Default, v.HasDefault
    if v.Tag.Has("builtin-default") {
        raw, present = v.Tag.Get("builtin-default"), true
    }
    if present {
        h.Default = raw
        if kind == "bool" {
            if b, err := strconv.ParseBool(raw); err == nil {
                h.Default = b
            }
        }
        if kind == "int" {
            if n, err := strconv.Atoi(raw); err == nil {
                h.Default = n
            }
        }
    }
    return h
}

func helpData(node *kong.Node, l *i18n.Localizer) helpResult {
    h := helpResult{Command: node.FullPath(), Arguments: []helpValue{}, Flags: []helpValue{}, Examples: [][]string{}, Commands: []helpChild{}}
    for _, arg := range node.Positional {
        h.Arguments = append(h.Arguments, describeValue(arg, l))
    }
    for _, group := range node.AllFlags(false) {
        for _, flag := range group {
            if flag.Hidden || flag.Name == "target" && targetForbidden(node.Path()) {
                continue
            }
            v := describeValue(flag.Value, l)
            if flag.Name == "target" && node.Path() == "status" {
                v.Description = l.Text("ps_flag_target")
                // History filters never inherit the project's selected target.
                v.Configurable, v.ConfigKey = false, ""
            }
            if flag.Name == "product" && node.Path() != "cloud search" && node.Path() != "cloud watch" && node.Path() != "cloud acquire" {
                // Non-acquisition calls resolve accounts or frozen bindings,
                // not resource-profile product defaults.
                v.ConfigKey = ""
            }
            if flag.Name == "json" && node.Path() == "ssh" {
                v.Description = l.Text("interactive_json")
            }
            if flag.Name == "json" && node.Path() == "forward" {
                v.Description = l.Text("forward_json")
            }
            if (flag.Name == "json" || flag.Name == "trace") && node.Path() == "notify channel" {
                v.Description = l.Text("notify_channel_native")
            }
            h.Flags = append(h.Flags, v)
        }
    }
    for _, child := range node.Children {
        if !child.Hidden {
            h.Commands = append(h.Commands, helpChild{child.Name, l.Text(child.Help)})
        }
    }
    if node.Parent == nil {
        h.Examples = append(h.Examples, []string{"loaf", "machine", "add", "lab", "--ssh", "lab-gpu"}, []string{"loaf", "machine", "use", "lab"}, []string{"loaf", "run", "--", "python3", "train.py"})
    } else if example := node.Tag.Get("example"); example != "" {
        h.Examples = append(h.Examples, strings.Fields(example))
    }
    var text strings.Builder
    renderHelp(&text, node, h, l)
    h.Help = text.String()
    return h
}

func printHelp(out io.Writer, node *kong.Node, l *i18n.Localizer) {
    fmt.Fprint(out, helpData(node, l).Help)
}

func renderHelp(out io.Writer, node *kong.Node, h helpResult, l *i18n.Localizer) {
    short := node.Help
    if short == "" {
        short = "root_short"
    }
    fmt.Fprintf(out, "%s\n\n%s\n  %s", l.Text(short), l.Text("usage"), node.FullPath())
    if args := node.Tag.Get("usage-args"); args != "" {
        fmt.Fprintf(out, " %s %s", l.Text("options_arg"), l.Text(args))
    } else if node.Path() == "wait" {
        fmt.Fprintf(out, " %s %s", l.Text("wait_args"), l.Text("options_arg"))
    } else {
        for _, arg := range h.Arguments {
            label := "<" + l.Text("type_"+arg.Type) + ">"
            if arg.Variadic {
                label += "..."
            }
            if !arg.Required {
                label = "[" + label + "]"
            }
            fmt.Fprint(out, " "+label)
        }
        fmt.Fprint(out, " "+l.Text("options_arg"))
    }
    fmt.Fprintln(out)
    if node.Parent == nil {
        for _, group := range []string{"experiments", "machines", "resources", "diagnostics"} {
            fmt.Fprintln(out, "\n"+l.Text("group_"+group))
            for _, child := range node.Children {
                if !child.Hidden && child.Tag.Group == group {
                    writeHelpLine(out, child.Name, l.Text(child.Help))
                }
            }
        }
    } else if len(h.Commands) > 0 {
        fmt.Fprintln(out, "\n"+l.Text("commands"))
        for _, child := range h.Commands {
            writeHelpLine(out, child.Name, child.Description)
        }
    }
    if len(h.Examples) > 0 {
        fmt.Fprintln(out, "\n"+l.Text("help_examples"))
        for _, example := range h.Examples {
            fmt.Fprintln(out, "  "+strings.Join(example, " "))
        }
    }
    grouped := node.Path() == "cloud acquire"
    if len(h.Flags) > 0 && !grouped {
        fmt.Fprintln(out, "\n"+l.Text("flags"))
    }
    flags := h.Flags
    if grouped {
        flags = nil
        for _, group := range []string{"common", "environment", "conditions", "advanced", "global"} {
            for _, flag := range h.Flags {
                current := flag.Group
                if current == "" {
                    current = "global"
                }
                if current == group {
                    flag.Group = group
                    flags = append(flags, flag)
                }
            }
        }
    }
    group := ""
    for _, flag := range flags {
        if grouped && group != flag.Group {
            group = flag.Group
            fmt.Fprintln(out, "\n"+l.Text("cloud_help_"+group))
        }
        label := "--" + flag.Name
        original := findFlag(node, label)
        if original.Short != 0 {
            label = "-" + string(original.Short) + ", " + label
        }
        if flag.Type != "bool" {
            label += " <" + l.Text("type_"+flag.Type) + ">"
        }
        description := flag.Description
        if flag.Default != nil {
            key := "default_value"
            if flag.Configurable {
                key = "builtin_default_value"
            }
            description += " (" + l.Text(key, map[string]any{"Value": flag.Default}) + ")"
        }
        writeHelpLine(out, label, description)
    }
}

func writeHelpLine(out io.Writer, label, description string) {
    fmt.Fprintf(out, "  %s%s\n", padFlag(label, max(26, displayWidth(label)+2)), description)
}

func displayWidth(s string) int {
    n := 0
    for _, r := range s {
        n++
        k := width.LookupRune(r).Kind()
        if k == width.EastAsianWide || k == width.EastAsianFullwidth {
            n++
        }
    }
    return n
}
func padFlag(s string, cells int) string {
    return s + strings.Repeat(" ", max(0, cells-displayWidth(s)))
}
