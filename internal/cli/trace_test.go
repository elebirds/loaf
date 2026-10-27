package cli

import (
    "encoding/json"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/ssh"
)

func TestTraceIsOptInFiniteAndLocalized(t *testing.T) {
    o, _ := runFixture(t)
    o.Options.Trace = &ssh.Trace{}
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "check", "--trace", "--lang", lang, "--json")
        var result struct {
            Trace *ssh.TraceReport `json:"trace"`
        }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &result) != nil || result.Trace == nil {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "check", "--lang", lang, "--json")
        if code != 0 || stderr != "" || strings.Contains(out, `"trace"`) {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "check", "--trace", "--lang", lang)
        if code != 0 || out == "" || stderr == "" || strings.Contains(stderr, "trace_summary") {
            t.Fatal(code, out, stderr)
        }
    }
    for _, args := range [][]string{{"logs", "-f", "--trace"}, {"exec", "--trace", "--", "true"}, {"ssh", "--trace"}, {"forward", "6006", "--trace"}, {"cloud", "watch", "--trace"}} {
        if code, out, stderr := run(t, o, append([]string{"--json"}, args...)...); code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatal(args, code, out, stderr)
        }
    }
}
