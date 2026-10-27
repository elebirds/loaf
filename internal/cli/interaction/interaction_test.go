package interaction

import (
    "bytes"
    "context"
    "errors"
    "io"
    "os"
    "strings"
    "testing"
    "time"
)

func TestNonInteractiveNeverReadsOrPrompts(t *testing.T) {
    input := strings.NewReader("yes\nsecret\n")
    var output bytes.Buffer
    session := New(Options{In: input, Err: &output, Rich: true})
    if _, err := session.Text(context.Background(), TextPrompt{Title: "Text"}); !errors.Is(err, ErrNonInteractive) {
        t.Fatal(err)
    }
    if _, err := session.Confirm(context.Background(), Confirmation{}); !errors.Is(err, ErrNonInteractive) {
        t.Fatal(err)
    }
    if _, err := session.Secret(context.Background(), SecretPrompt{}); !errors.Is(err, ErrNonInteractive) {
        t.Fatal(err)
    }
    if input.Len() != len("yes\nsecret\n") || output.Len() != 0 {
        t.Fatal("disabled prompt consumed or emitted data")
    }
    value, err := session.ReadSecretLine(context.Background(), 4096)
    defer clear(value)
    if err != nil || string(value) != "yes" || output.Len() != 0 {
        t.Fatal("explicit stdin must work without prompting", err)
    }
}

func TestPlainLoginHasOneInputOwner(t *testing.T) {
    input := strings.NewReader("student\n raw password \r\nverification\n")
    var output bytes.Buffer
    session := New(Options{In: input, Err: &output, Interactive: true, Rich: true})
    name, err := session.Plain().Text(context.Background(), TextPrompt{Title: "Username: ", MaxBytes: 512})
    if err != nil || name != "student" {
        t.Fatal(name, err)
    }
    password, err := session.ReadSecretLine(context.Background(), 4096)
    defer clear(password)
    if err != nil || string(password) != " raw password " {
        t.Fatal("password bytes were changed or consumed by username", err)
    }
    code, err := session.ReadSecretLine(context.Background(), 4096)
    defer clear(code)
    if err != nil || string(code) != "verification" || output.String() != "Username: " {
        t.Fatal("input was read ahead or stdout-style data was emitted", err, output.String())
    }
}

func TestConfirmationStrength(t *testing.T) {
    for _, test := range []struct {
        answer          string
        ordinary, typed bool
    }{{"\n", false, false}, {"", false, false}, {"y\n", true, false}, {"YES\n", true, false}, {"是\n", true, false}, {" yes \n", true, true}} {
        for _, kind := range []ConfirmationKind{ConfirmDefaultNo, ConfirmTypedYes} {
            s := New(Options{In: strings.NewReader(test.answer), Interactive: true})
            got, err := s.Confirm(context.Background(), Confirmation{Kind: kind})
            want := test.ordinary
            if kind == ConfirmTypedYes {
                want = test.typed
            }
            if err != nil || got != want {
                t.Fatalf("%q kind=%d: got %t/%v, want %t", test.answer, kind, got, err, want)
            }
        }
    }
}

func TestSecretLineBoundsAndEOF(t *testing.T) {
    for _, test := range []struct {
        input, want string
        failure     error
    }{{"abcd\r\nnext", "abcd", nil}, {"abcd", "abcd", nil}, {"abcd\r", "abcd", nil}, {"abcde\n", "", ErrInputTooLong}, {"\n", "", ErrEmptySecret}, {"", "", io.EOF}} {
        s := New(Options{In: strings.NewReader(test.input)})
        value, err := s.ReadSecretLine(context.Background(), 4)
        if !errors.Is(err, test.failure) || string(value) != test.want {
            t.Fatalf("bounded secret result: len=%d err=%v", len(value), err)
        }
        clear(value)
    }
}

func TestSecretFailureClearsReturnedBuffer(t *testing.T) {
    for _, failure := range []error{io.ErrUnexpectedEOF, context.Canceled, nil} {
        value := []byte("oversized")
        s := New(Options{Interactive: true, ReadPassword: func(context.Context) ([]byte, error) { return value, failure }})
        got, err := s.Secret(context.Background(), SecretPrompt{MaxBytes: 4})
        if err == nil || got != nil || !bytes.Equal(value, make([]byte, len(value))) {
            t.Fatal("failed secret retained bytes", err)
        }
    }
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return 0, nil }

func TestPromptWriteFailureDoesNotRead(t *testing.T) {
    input := strings.NewReader("yes\n")
    s := New(Options{In: input, Err: shortWriter{}, Interactive: true})
    if _, err := s.Confirm(context.Background(), Confirmation{Title: "Proceed? "}); !errors.Is(err, io.ErrShortWrite) || input.Len() != 4 {
        t.Fatal(err, input.Len())
    }
}

func TestFileInputCancellationReleasesOwner(t *testing.T) {
    read, write, err := os.Pipe()
    if err != nil {
        t.Fatal(err)
    }
    defer read.Close()
    defer write.Close()
    s := New(Options{In: read})
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
    defer cancel()
    if _, err := s.ReadSecretLine(ctx, 4096); !errors.Is(err, context.DeadlineExceeded) {
        t.Fatal(err)
    }
    if _, err := io.WriteString(write, "next\n"); err != nil {
        t.Fatal(err)
    }
    value, err := s.ReadSecretLine(context.Background(), 4096)
    defer clear(value)
    if err != nil || string(value) != "next" {
        t.Fatal("canceled read kept consuming stdin", err)
    }
    if err := read.Close(); err != nil {
        t.Fatal(err)
    }
    if _, err := s.ReadSecretLine(context.Background(), 4096); !errors.Is(err, os.ErrClosed) {
        t.Fatal("closed input must not block", err)
    }
}
