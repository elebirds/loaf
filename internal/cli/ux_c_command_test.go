package cli

import (
    "context"
    "errors"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/fault"
)

func TestUXCCommandLiteralRoundTrip(t *testing.T) {
    cases := [][]string{
        {"python3", "a b.py", "中文", "", "--", "$HOME", ";", "'", `a\b`, `a"b`, "*.pt", "~/data", "{a,b}"},
        {"printf", "line\nnext\tend", "--json", "a=b", "#literal"},
    }
    for _, args := range cases {
        got, err := uxCParseArgv(shellCommand(args))
        if err != nil || !reflect.DeepEqual(got, args) {
            t.Fatal(args, got, err)
        }
    }
    got, err := uxCParseArgv(`python3 a\ b.py "中 文" '' \$HOME \; -- "a\qb"`)
    want := []string{"python3", "a b.py", "中 文", "", "$HOME", ";", "--", `a\qb`}
    if err != nil || !reflect.DeepEqual(got, want) {
        t.Fatal(got, err)
    }
    for _, text := range []string{"", " ", "''", "A=secret echo x", "echo x;", "echo x; echo y", "echo x\necho y", "echo x # secret", "# secret\necho x", "echo x &", "echo x | cat", "echo x > path", "! true", "echo $HOME", "echo ${HOME}", "echo $(touch sentinel)", "echo `pwd`", `echo "$HOME"`, "echo *.pt", "echo ~/data", "echo {a,b}", "echo $'word'", "echo \x00"} {
        _, err := uxCParseArgv(text)
        var f *fault.Error
        if !errors.As(err, &f) || f.Code != "BAD_CONFIG" {
            t.Fatal("accepted nonliteral input", text, err)
        }
        if err != nil && strings.Contains(err.Error(), "secret") {
            t.Fatal("parser error leaked input", err)
        }
    }
    script := `printf '%s' "$HOME"; echo '中 文'`
    got, err = uxCShellArgs(script)
    if err != nil || !reflect.DeepEqual(got, []string{"/bin/sh", "-c", script}) {
        t.Fatal(got, err)
    }
    for _, text := range []string{"", "\t\n", "echo \x00"} {
        if _, err := uxCShellArgs(text); err == nil {
            t.Fatal("accepted empty/invalid script")
        }
    }
}

func TestUXCCommandEditorKeepsDraftAndDistinguishesProject(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, test := range []struct {
            name, input   string
            initial, want []string
            canceled      bool
        }{
            {"keep", "1\n", []string{"printf", "", "$HOME", ";", "--", "中 文"}, []string{"printf", "", "$HOME", ";", "--", "中 文"}, false},
            {"project", "1\n", nil, nil, false},
            {"argv", "2\npython3 'a b.py' '$HOME' ';' --\n", nil, []string{"python3", "a b.py", "$HOME", ";", "--"}, false},
            {"shell", "3\nprintf '%s' \"$HOME\"; true\n\n", nil, []string{"/bin/sh", "-c", `printf '%s' "$HOME"; true`}, false},
            {"back", "5\n", []string{"echo", "old"}, nil, true},
        } {
            t.Run(language+"/"+test.name, func(t *testing.T) {
                c := uxCCall(t, fixture(t), language, test.input, "run")
                got, err := c.uxCEditCommand(context.Background(), test.initial)
                if test.canceled {
                    if !errors.Is(err, interaction.ErrCanceled) {
                        t.Fatal(err)
                    }
                    return
                }
                if err != nil || !reflect.DeepEqual(got, test.want) {
                    t.Fatal(got, err)
                }
                if test.initial != nil && got != nil {
                    got[0] = "changed"
                    if test.initial[0] == "changed" {
                        t.Fatal("aliased draft")
                    }
                }
            })
        }
    }
    c := uxCCall(t, fixture(t), "en", "", "run")
    if _, err := c.uxCEditCommand(context.Background(), []string{}); err == nil {
        t.Fatal("explicit empty command accepted")
    }
    c.policy.Batch = true
    if _, err := c.uxCEditCommand(context.Background(), nil); !errors.Is(err, interaction.ErrNonInteractive) {
        t.Fatal(err)
    }
}
