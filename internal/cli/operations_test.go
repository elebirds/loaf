package cli

import (
    "bytes"
    "context"
    "encoding/base64"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

type fakeOperations struct {
    target        config.Target
    input         ssh.ExecInput
    result        ssh.ExecResult
    err           error
    output        []byte
    local, remote int
    login         bool
    calls         int
}

func (f *fakeOperations) Exec(_ context.Context, target config.Target, input ssh.ExecInput, _ io.Reader, stdout, stderr io.Writer) (ssh.ExecResult, error) {
    f.calls++
    f.target, f.input = target, input
    if len(f.output) == 0 {
        f.output = []byte("live output\n")
    }
    if _, err := stdout.Write(f.output); err != nil {
        return ssh.ExecResult{}, err
    }
    if _, err := io.WriteString(stderr, "program diagnostic\n"); err != nil {
        return ssh.ExecResult{}, err
    }
    if f.result.Status == "" {
        zero := 0
        f.result = ssh.ExecResult{Status: "succeeded", ExitCode: &zero}
    }
    return f.result, f.err
}

func (f *fakeOperations) Interactive(_ context.Context, target config.Target, _ io.Reader, _, _ io.Writer) error {
    f.calls++
    f.target, f.login = target, true
    return f.err
}

func (f *fakeOperations) Forward(_ context.Context, target config.Target, local, remote int, _ bool, _ io.Writer) error {
    f.calls++
    f.target, f.local, f.remote = target, local, remote
    return f.err
}

func operationFixture(t *testing.T) (Options, *fakeOperations) {
    t.Helper()
    o := fixture(t)
    f := &fakeOperations{}
    o.Operations = f
    if err := o.Store.Add(context.Background(), "demo", config.Target{Destination: "gpu", RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    return o, f
}

func TestExecUsesMachineWithoutProjectStateOrSync(t *testing.T) {
    o, f := operationFixture(t)
    argv := []string{"printf", "%s", "a b", "$(touch no)", "", "--json", "--lang"}
    code, out, diagnostic := run(t, o, append([]string{"exec", "--target", "demo", "--"}, argv...)...)
    if code != 0 || out != "live output\n" || diagnostic != "program diagnostic\n" || f.input.CWD != "" || !f.input.Batch || f.target.Destination != "gpu" || !reflect.DeepEqual(f.input.Args, argv) {
        t.Fatal(code, out, diagnostic, f)
    }
    if _, err := os.Stat(o.Store.IndexFile); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("exec created project state", err)
    }
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("target = 'demo'\n[exec]\ncwd = 'project folder'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    code, _, _ = run(t, o, "exec", "--", "pwd")
    if code != 0 || f.input.CWD != "project folder" {
        t.Fatal(f.input, code)
    }
    code, _, _ = run(t, o, "exec", "--cwd", "", "--", "pwd")
    if code != 0 || f.input.CWD != "" {
        t.Fatal("empty flag must restore home", f.input, code)
    }
}

func TestExecJSONStreamsAndOperationExits(t *testing.T) {
    for _, tc := range []struct {
        remote int
        status string
        fault  string
        code   int
    }{{0, "succeeded", "", 0}, {7, "failed", "EXEC_FAILED", 1}, {-1, "unconfirmed", "EXEC_UNCONFIRMED", 3}} {
        for _, lang := range []string{"en", "zh-Hans"} {
            o, f := operationFixture(t)
            f.result.Status = tc.status
            if tc.remote >= 0 {
                f.result.ExitCode = &tc.remote
            }
            if tc.fault != "" {
                f.err = fault.New(tc.fault, map[bool]string{true: "exec_failed", false: "exec_unconfirmed"}[tc.remote >= 0], map[string]any{"Value": tc.remote})
            }
            f.output = append(bytes.Repeat([]byte("中文 text\n"), 9000), 0xff, 0xfe)
            code, out, diagnostic := run(t, o, "exec", "--lang", lang, "--target", "demo", "--json", "--", "printf", "x")
            if code != tc.code || diagnostic != "" {
                t.Fatal(code, out, diagnostic)
            }
            decoder := json.NewDecoder(strings.NewReader(out))
            reconstructed := map[string][]byte{}
            chunks, ends := 0, 0
            for {
                var e struct {
                    SchemaVersion int             `json:"schema_version"`
                    OK            bool            `json:"ok"`
                    Data          json.RawMessage `json:"data"`
                    Error         *errorView      `json:"error"`
                }
                if err := decoder.Decode(&e); errors.Is(err, io.EOF) {
                    break
                } else if err != nil {
                    t.Fatal(err, out)
                }
                if e.SchemaVersion != 1 {
                    t.Fatal(e)
                }
                var event execChunk
                if err := json.Unmarshal(e.Data, &event); err != nil {
                    t.Fatal(err)
                }
                if event.Type == "chunk" {
                    chunks++
                    if !e.OK || e.Error != nil || event.Offset != int64(len(reconstructed[event.Stream])) {
                        t.Fatal(e, event)
                    }
                    data := []byte(event.Data)
                    if event.Encoding == "base64" {
                        var err error
                        data, err = base64.StdEncoding.DecodeString(event.Data)
                        if err != nil {
                            t.Fatal(err)
                        }
                    } else if event.Encoding != "utf-8" {
                        t.Fatal(event)
                    }
                    if len(data) > 32<<10 {
                        t.Fatal("unbounded chunk", len(data))
                    }
                    reconstructed[event.Stream] = append(reconstructed[event.Stream], data...)
                    continue
                }
                ends++
                var final execResult
                if err := json.Unmarshal(e.Data, &final); err != nil {
                    t.Fatal(err)
                }
                if final.Status != tc.status || final.Target != "demo" || tc.fault == "" && (final.Type != "end" || !e.OK) || tc.fault != "" && (final.Type != "error" || e.Error == nil || e.Error.Code != tc.fault) {
                    t.Fatal(e, final)
                }
            }
            if chunks < 4 || ends != 1 || !bytes.Equal(reconstructed["stdout"], f.output) || string(reconstructed["stderr"]) != "program diagnostic\n" {
                t.Fatal(chunks, ends, reconstructed)
            }
        }
    }
}

func TestMachineOperationFlagsAndInteractiveContracts(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, command := range []string{"exec", "ssh", "forward"} {
            o, _ := operationFixture(t)
            code, out, diagnostic := run(t, o, command, "--lang", lang, "--help")
            if code != 0 || out == "" || diagnostic != "" || strings.Contains(out, "<no value>") {
                t.Fatal(code, out, diagnostic)
            }
            if command == "exec" && lang == "en" && !strings.Contains(out, "loaf exec [options] -- <command>") {
                t.Fatal("help must put Loaf flags before the argv delimiter", out)
            }
            if command == "forward" && (strings.Contains(out, "default: 0") || strings.Contains(out, "默认：0")) {
                t.Fatal("zero is an internal sentinel, not the default local port", out)
            }
        }
    }
    for _, args := range [][]string{
        {"exec", "--target", "demo"}, {"exec", "--target", "demo", "printf", "x"},
        {"ssh", "--json"}, {"ssh"}, {"forward", "6006", "--json"}, {"forward", "0"},
        {"forward", "65536"}, {"forward", "6006", "--local-port", "0"}, {"forward", "6006", "--local-port", "65536"},
    } {
        o, f := operationFixture(t)
        code, _, _ := run(t, o, args...)
        if code != 2 || f.calls != 0 {
            t.Fatal(args, code, f)
        }
    }
    o, f := operationFixture(t)
    o.Interactive = true
    code, _, diagnostic := run(t, o, "ssh", "--target", "demo")
    if code != 0 || diagnostic != "" || !f.login || f.target.Destination != "gpu" {
        t.Fatal(code, diagnostic, f)
    }
    for _, args := range [][]string{{"forward", "6006", "--target", "demo"}, {"forward", "6006", "--local-port", "16006", "--target", "demo"}} {
        o, f := operationFixture(t)
        code, out, diagnostic := run(t, o, args...)
        want := 6006
        if len(args) > 4 {
            want = 16006
        }
        if code != 0 || out != "" || diagnostic == "" || f.local != want || f.remote != 6006 || f.target.Destination != "gpu" {
            t.Fatal(code, out, diagnostic, f)
        }
    }
}
