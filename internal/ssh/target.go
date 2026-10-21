// Package ssh adapts system OpenSSH without replacing user connection configuration.
package ssh

import (
    "fmt"
    "net/url"
    "regexp"
    "strings"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "mvdan.cc/sh/v3/syntax"
)

// ParseConnection accepts a connection command or Host alias, never Shell code.
func ParseConnection(input string) (config.Target, error) {
    bad := func() (config.Target, error) { return config.Target{}, fault.New("BAD_CONFIG", "ssh_input", nil) }
    f, err := syntax.NewParser().Parse(strings.NewReader(input), "")
    if err != nil || len(f.Stmts) != 1 {
        return bad()
    }
    s := f.Stmts[0]
    if s.Background || s.Negated || s.Coprocess || len(s.Redirs) != 0 {
        return bad()
    }
    call, ok := s.Cmd.(*syntax.CallExpr)
    if !ok || len(call.Assigns) != 0 {
        return bad()
    }
    var args []string
    for _, word := range call.Args {
        value, err := literal(word.Parts, false)
        if err != nil {
            return bad()
        }
        args = append(args, value)
    }
    if len(args) > 0 && args[0] == "ssh" {
        args = args[1:]
    }
    t, err := connectionArgs(args)
    if err != nil {
        return bad()
    }
    return t, nil
}

func literal(parts []syntax.WordPart, quoted bool) (string, error) {
    var b strings.Builder
    for _, part := range parts {
        switch p := part.(type) {
        case *syntax.Lit:
            for i := 0; i < len(p.Value); i++ {
                c := p.Value[i]
                if c == '\\' && i+1 < len(p.Value) {
                    next := p.Value[i+1]
                    if !quoted || strings.ContainsRune("$`\"\\\n", rune(next)) {
                        i++
                        if next != '\n' {
                            b.WriteByte(next)
                        }
                        continue
                    }
                }
                b.WriteByte(c)
            }
        case *syntax.SglQuoted:
            if p.Dollar {
                return "", fmt.Errorf("expansion")
            }
            b.WriteString(p.Value)
        case *syntax.DblQuoted:
            if p.Dollar {
                return "", fmt.Errorf("expansion")
            }
            s, err := literal(p.Parts, true)
            if err != nil {
                return "", err
            }
            b.WriteString(s)
        default:
            return "", fmt.Errorf("expansion")
        }
    }
    return b.String(), nil
}

var destinationPattern = regexp.MustCompile(`^[A-Za-z0-9_\[\]][A-Za-z0-9_.@:\[\]-]*$`)

func connectionArgs(args []string) (config.Target, error) {
    t := config.Target{RemoteBase: "~/.local/share/loaf/projects"}
    valueOptions := map[string]bool{"-p": true, "-i": true, "-F": true, "-J": true, "-l": true, "-o": true}
    for i := 0; i < len(args); i++ {
        a := args[i]
        if strings.ContainsAny(a, "\x00\r\n") || a == "" {
            return t, fmt.Errorf("invalid argument")
        }
        if a == "--" {
            if i+2 != len(args) {
                return t, fmt.Errorf("destination required")
            }
            t.Destination = args[i+1]
            break
        }
        if !strings.HasPrefix(a, "-") {
            if i != len(args)-1 {
                return t, fmt.Errorf("remote command")
            }
            t.Destination = a
            break
        }
        if a == "-4" || a == "-6" || a == "-q" || a == "-v" || a == "-vv" || a == "-vvv" {
            t.Args = append(t.Args, a)
            continue
        }
        option, value := a, ""
        if len(a) > 2 {
            option, value = a[:2], a[2:]
        }
        if !valueOptions[option] {
            return t, fmt.Errorf("unsupported option")
        }
        if value == "" {
            i++
            if i >= len(args) {
                return t, fmt.Errorf("missing value")
            }
            value = args[i]
        }
        if value == "" || strings.ContainsAny(value, "\x00\r\n") {
            return t, fmt.Errorf("invalid value")
        }
        if option == "-p" {
            var port int
            if _, err := fmt.Sscanf(value, "%d", &port); err != nil || fmt.Sprint(port) != value || port < 1 || port > 65535 {
                return t, fmt.Errorf("invalid port")
            }
        }
        if option == "-o" && !safeOption(value) {
            return t, fmt.Errorf("unsafe option")
        }
        t.Args = append(t.Args, option, value)
    }
    if strings.HasPrefix(t.Destination, "ssh://") {
        u, err := url.Parse(t.Destination)
        if err != nil || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
            return t, fmt.Errorf("invalid SSH URL")
        }
        if u.User != nil {
            if _, ok := u.User.Password(); ok {
                return t, fmt.Errorf("password")
            }
        }
    } else if !destinationPattern.MatchString(t.Destination) {
        return t, fmt.Errorf("invalid destination")
    }
    return t, nil
}

func safeOption(value string) bool {
    parts := strings.FieldsFunc(value, func(r rune) bool { return r == '=' || r == ' ' || r == '\t' })
    if len(parts) < 2 {
        return false
    }
    key := strings.ToLower(parts[0])
    if key == "stricthostkeychecking" {
        return len(parts) == 2 && (parts[1] == "yes" || parts[1] == "ask")
    }
    if key == "userknownhostsfile" || key == "globalknownhostsfile" {
        return !strings.Contains(value, "/dev/null") && !strings.EqualFold(parts[1], "none")
    }
    return map[string]bool{"proxyjump": true, "connecttimeout": true, "connectionattempts": true, "serveraliveinterval": true, "serveralivecountmax": true, "identitiesonly": true, "identityfile": true, "identityagent": true, "port": true, "user": true, "hostname": true, "addressfamily": true, "loglevel": true}[key]
}

// Validate rechecks manually edited connection configuration before invoking SSH.
func Validate(t config.Target) error {
    if t.Cloud != nil {
        if !config.ValidCloudBinding(*t.Cloud) || t.Destination != "" {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        // Local SSH preferences may accompany the binding, but the endpoint is
        // always refreshed from the same immutable cloud instance.
        t.Destination = "bound-instance"
    }
    if _, err := connectionArgs(append(append([]string{}, t.Args...), t.Destination)); err != nil {
        return fault.New("BAD_CONFIG", "ssh_input", nil)
    }
    return nil
}

// Quote encodes one literal word for the remote POSIX Shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
