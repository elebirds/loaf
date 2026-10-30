package ssh

import (
    "encoding/json"
    "fmt"
    "io"
    "os"
    "strings"
    "testing"
)

// A Go subprocess fixture exercises the real exec/pipe/cancellation boundary on
// every client OS. Shell execution belongs only in remote Linux shell tests.
type processFixture struct {
    Stdout, Stderr   string
    Exit             int
    Count, Arguments string
    Retry, Completed string
}

func fixtureProcess(t *testing.T, fixture processFixture) string {
    t.Helper()
    data, err := json.Marshal(fixture)
    if err != nil {
        t.Fatal(err)
    }
    t.Setenv("LOAF_SSH_PROCESS_FIXTURE", string(data))
    executable, err := os.Executable()
    if err != nil {
        t.Fatal(err)
    }
    return executable
}

func TestMain(m *testing.M) {
    if data := os.Getenv("LOAF_SSH_PROCESS_FIXTURE"); data != "" {
        var fixture processFixture
        if err := json.Unmarshal([]byte(data), &fixture); err != nil {
            os.Exit(90)
        }
        if len(os.Args) > 1 && os.Args[1] == "--version" {
            fmt.Println("rsync version fixture")
            os.Exit(0)
        }
        if len(os.Args) > 1 && os.Args[1] == "-u" {
            fmt.Println("/fixture-local")
            os.Exit(0)
        }
        first := true
        if fixture.Count != "" {
            _, err := os.Stat(fixture.Count)
            first = os.IsNotExist(err)
            file, err := os.OpenFile(fixture.Count, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
            if err != nil {
                os.Exit(91)
            }
            fmt.Fprintln(file, "call")
            file.Close()
        }
        if fixture.Arguments != "" {
            if err := os.WriteFile(fixture.Arguments, []byte(strings.Join(os.Args[1:], "\n")), 0600); err != nil {
                os.Exit(92)
            }
        }
        io.Copy(io.Discard, os.Stdin)
        if fixture.Retry == "download" {
            if first {
                os.WriteFile(fixture.Completed, []byte("preserved"), 0600)
                fmt.Fprintln(os.Stderr, "Connection closed by fixture: unexpected end of file")
                os.Exit(255)
            }
            content, err := os.ReadFile(fixture.Completed)
            if err != nil || string(content) != "preserved" {
                os.Exit(93)
            }
            fmt.Println("LOAF_CHANGE:>f+++++++++")
            os.Exit(0)
        }
        if fixture.Retry == "response" && !first {
            fmt.Fprintln(os.Stderr, "Connection refused")
            os.Exit(255)
        }
        fmt.Fprint(os.Stdout, fixture.Stdout)
        fmt.Fprint(os.Stderr, fixture.Stderr)
        os.Exit(fixture.Exit)
    }
    os.Exit(m.Run())
}
