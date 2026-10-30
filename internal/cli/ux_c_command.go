package cli

import (
    "context"
    "errors"
    "fmt"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "mvdan.cc/sh/v3/syntax"
)

// uxCEditCommand returns nil for the project command. An existing argv can be
// kept byte-for-byte, including empty arguments and Shell-looking literals.
// Back is ErrCanceled; callers retain their draft rather than replacing it.
func (c *invocation) uxCEditCommand(ctx context.Context, initial []string) ([]string, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    if initial != nil {
        if err := uxCValidateArgs(initial); err != nil {
            return nil, err
        }
    }
    l := c.output.locale
    choices := []interaction.Choice{}
    if initial != nil {
        choices = append(choices, interaction.Choice{Label: l.Text("ux_c_command_keep"), Value: "keep"})
    }
    choices = append(choices,
        interaction.Choice{Label: l.Text("ux_c_command_project"), Value: "project"},
        interaction.Choice{Label: l.Text("ux_c_command_argv"), Value: "argv"},
        interaction.Choice{Label: l.Text("ux_c_command_shell"), Value: "shell"},
        interaction.Choice{Label: l.Text("ux_core_back"), Value: "back"},
    )
    selected := "keep"
    if initial == nil {
        selected = "argv"
    }
    summary := ""
    if initial != nil {
        summary = c.uxCSummaryLines(fmt.Sprintf("%q", initial))
    }
    kind, err := c.input.SelectWithSummary(ctx, l.Text("ux_c_command_title"), summary, choices, selected)
    if err != nil {
        return nil, err
    }
    switch kind {
    case "keep":
        return append([]string{}, initial...), nil
    case "project":
        return nil, nil
    case "back":
        return nil, interaction.ErrCanceled
    }
    prompt := interaction.TextPrompt{Title: l.Text("ux_c_command_argv") + ": ", Description: l.Text("ux_c_argv_help"), MaxBytes: 4096}
    previous := ""
    if initial != nil {
        previous = shellCommand(initial)
    }
    parse := uxCParseArgv
    if kind == "shell" {
        prompt.Title, prompt.Description = "/bin/sh -c: ", l.Text("ux_c_shell_help")
        previous = ""
        if len(initial) == 3 && initial[0] == "/bin/sh" && initial[1] == "-c" {
            previous = initial[2]
        }
        parse = uxCShellArgs
    }
    if previous != "" {
        prompt.Description += "\n" + l.Text("ux_c_command_blank_keeps")
    }
    prompt.Validate = func(value string) error {
        if value == "" && previous != "" {
            return nil
        }
        if _, err := parse(value); err != nil {
            return errors.New(l.Text("ux_c_argv_invalid"))
        }
        return nil
    }
    value, err := c.input.Text(ctx, prompt)
    if err != nil {
        return nil, err
    }
    if value == "" {
        value = previous
    }
    args, err := parse(value)
    if err != nil {
        return nil, err
    }
    if kind == "shell" {
        if err := c.uxShowDocument(ctx, interaction.Document{Title: l.Text("ux_c_shell_review"), Context: "/bin/sh -c", Body: args[2]}); err != nil {
            return nil, err
        }
    }
    return args, nil
}

func uxCValidateArgs(args []string) error {
    _, err := execution.ResolveExecution(config.ProjectFile{}, args)
    return err
}

func uxCShellArgs(script string) ([]string, error) {
    if strings.TrimSpace(script) == "" {
        return nil, fault.Invalid("command", "invalid_value", "ux_c_argv_invalid")
    }
    args := []string{"/bin/sh", "-c", script}
    return args, uxCValidateArgs(args)
}

// uxCParseArgv accepts one literal POSIX-style argument list, not a program.
// Quotes and backslashes group literal bytes; operators, comments, assignments
// and expansion syntax must instead be quoted or entered in explicit Shell mode.
func uxCParseArgv(line string) ([]string, error) {
    bad := func() ([]string, error) {
        return nil, fault.Invalid("command", "invalid_value", "ux_c_argv_invalid")
    }
    if strings.ContainsRune(line, 0) {
        return bad()
    }
    // Recognize Bash extensions too so $'...' cannot become an innocent '$'
    // followed by a POSIX quote. Only the literal AST subset below is accepted.
    file, err := syntax.NewParser(syntax.KeepComments(true)).Parse(strings.NewReader(line), "")
    if err != nil || len(file.Stmts) != 1 || len(file.Last) != 0 {
        return bad()
    }
    stmt := file.Stmts[0]
    if stmt.Background || stmt.Negated || stmt.Coprocess || stmt.Semicolon.IsValid() || len(stmt.Redirs) != 0 || len(stmt.Comments) != 0 {
        return bad()
    }
    call, ok := stmt.Cmd.(*syntax.CallExpr)
    if !ok || len(call.Assigns) != 0 {
        return bad()
    }
    args := make([]string, 0, len(call.Args))
    for _, word := range call.Args {
        value, ok := uxCLiteral(word.Parts, false)
        if !ok {
            return bad()
        }
        args = append(args, value)
    }
    if err := uxCValidateArgs(args); err != nil {
        return bad()
    }
    return args, nil
}

func uxCLiteral(parts []syntax.WordPart, quoted bool) (string, bool) {
    var value strings.Builder
    for _, part := range parts {
        switch p := part.(type) {
        case *syntax.Lit:
            for i := 0; i < len(p.Value); i++ {
                b := p.Value[i]
                if b == '\\' && i+1 < len(p.Value) {
                    next := p.Value[i+1]
                    if !quoted || strings.ContainsRune("$`\"\\\n", rune(next)) {
                        i++
                        if next != '\n' {
                            value.WriteByte(next)
                        }
                        continue
                    }
                }
                // The parser leaves globs, tilde and some brace expansions as
                // literals. Reject them unquoted instead of guessing intent.
                if !quoted && strings.ContainsRune("*?[]{}~", rune(b)) {
                    return "", false
                }
                value.WriteByte(b)
            }
        case *syntax.SglQuoted:
            if p.Dollar {
                return "", false
            }
            value.WriteString(p.Value)
        case *syntax.DblQuoted:
            if p.Dollar {
                return "", false
            }
            text, ok := uxCLiteral(p.Parts, true)
            if !ok {
                return "", false
            }
            value.WriteString(text)
        default:
            return "", false
        }
    }
    return value.String(), true
}
