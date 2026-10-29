// Package interaction owns command-scoped prompts and stdin consumption.
// Business decisions, translations, progress, and native command streams remain
// with the caller; prompts never write to stdout or open a replacement terminal.
package interaction

import (
    "context"
    "errors"
    "io"
    "os"
    "strings"
    "sync/atomic"

    "golang.org/x/term"
)

var (
    ErrNonInteractive   = errors.New("interactive input is disabled")
    ErrInputTooLong     = errors.New("input exceeds its byte limit")
    ErrEmptySecret      = errors.New("secret input is empty")
    ErrTerminalRequired = errors.New("secret input requires a terminal")
    ErrCanceled         = errors.New("prompt canceled")
)

// Prompter collects only the input requested by its caller.
type Prompter interface {
    Text(context.Context, TextPrompt) (string, error)
    Confirm(context.Context, Confirmation) (bool, error)
    // Secret returns an owned buffer. The caller must clear it after use.
    Secret(context.Context, SecretPrompt) ([]byte, error)
}

type TextPrompt struct {
    Title, Description string
    MaxBytes           int
    // Validate checks local input; it must not perform external operations.
    Validate func(string) error
}

type SecretPrompt struct {
    Title    string
    MaxBytes int
}

type ConfirmationKind uint8

const (
    ConfirmDefaultNo ConfirmationKind = iota
    ConfirmTypedYes
)

type Confirmation struct {
    Title, Description       string
    Summary                  *Document
    Kind                     ConfirmationKind
    AcceptLabel, RejectLabel string
}

// Options explicitly separates permission to prompt from rich rendering.
// ReadPassword is an optional terminal adapter, primarily for composition/tests.
// Blocking real inputs must be *os.File; other Readers are for in-memory input.
type Options struct {
    Compact        bool
    PageHelp       string
    SelectHelp     string
    MultiHelp      string
    PlainMultiHelp string
    ChoiceError    string
    In             io.Reader
    Err            io.Writer
    Interactive    bool
    Rich           bool
    ReadPassword   func(context.Context) ([]byte, error)
}

// InputSession serializes all prompt and explicit stdin reads for one command.
// Do not read its underlying input concurrently or give it to native commands
// until the session has finished. It never closes caller-owned streams.
type InputSession struct {
    dark    atomic.Bool
    options Options
    owner   chan struct{}
}

func New(options Options) *InputSession {
    if options.In == nil {
        options.In = strings.NewReader("")
    }
    if options.Err == nil {
        options.Err = io.Discard
    }
    if options.Rich {
        input, inputOK := options.In.(terminalOutput)
        output, outputOK := options.Err.(terminalOutput)
        options.Rich = inputOK && outputOK && term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd()))
    }
    session := &InputSession{options: options, owner: make(chan struct{}, 1)}
    session.dark.Store(true)
    return session
}

// Plain shares the session's input owner, but never starts a raw-mode renderer.
// Use it for the whole login conversation, including the non-secret username.
func (s *InputSession) Plain() Prompter { return plainPrompter{s} }

type plainPrompter struct{ session *InputSession }

func (p plainPrompter) Text(ctx context.Context, prompt TextPrompt) (string, error) {
    return p.session.text(ctx, prompt, false)
}
func (p plainPrompter) Confirm(ctx context.Context, prompt Confirmation) (bool, error) {
    return p.session.confirm(ctx, prompt, false)
}
func (p plainPrompter) Secret(ctx context.Context, prompt SecretPrompt) ([]byte, error) {
    return p.session.Secret(ctx, prompt)
}

func (s *InputSession) acquire(ctx context.Context, interactive bool) error {
    if err := ctx.Err(); err != nil {
        return err
    }
    if interactive && !s.options.Interactive {
        return ErrNonInteractive
    }
    select {
    case s.owner <- struct{}{}:
        if err := ctx.Err(); err != nil {
            s.release()
            return err
        }
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
func (s *InputSession) release() { <-s.owner }

func (s *InputSession) Text(ctx context.Context, prompt TextPrompt) (string, error) {
    return s.text(ctx, prompt, s.options.Rich)
}

func (s *InputSession) text(ctx context.Context, prompt TextPrompt, rich bool) (string, error) {
    if err := s.acquire(ctx, true); err != nil {
        return "", err
    }
    defer s.release()
    return s.textOwned(ctx, prompt, rich)
}

func (s *InputSession) textOwned(ctx context.Context, prompt TextPrompt, rich bool) (string, error) {
    limit := byteLimit(prompt.MaxBytes)
    if rich {
        return s.richText(ctx, prompt, limit)
    }
    for {
        if err := s.printPrompt(prompt.Title, prompt.Description); err != nil {
            return "", err
        }
        value, err := readLine(ctx, s.options.In, limit)
        text := string(value)
        clear(value)
        if err != nil {
            return "", err
        }
        if prompt.Validate != nil {
            if err := prompt.Validate(text); err != nil {
                if err := writeText(s.options.Err, err.Error()+"\n"); err != nil {
                    return "", err
                }
                continue
            }
        }
        return text, nil
    }
}

func (s *InputSession) Confirm(ctx context.Context, prompt Confirmation) (bool, error) {
    return s.confirm(ctx, prompt, s.options.Rich)
}

func (s *InputSession) confirm(ctx context.Context, prompt Confirmation, rich bool) (bool, error) {
    if err := s.acquire(ctx, true); err != nil {
        return false, err
    }
    defer s.release()
    if prompt.Kind != ConfirmDefaultNo && prompt.Kind != ConfirmTypedYes {
        return false, errors.New("unknown confirmation kind")
    }
    if rich && prompt.Kind == ConfirmDefaultNo && prompt.AcceptLabel != "" && prompt.RejectLabel != "" {
        return s.richConfirm(ctx, prompt)
    }
    var answer string
    var err error
    if rich && prompt.Kind == ConfirmTypedYes && prompt.Summary != nil {
        answer, err = s.richSummaryConfirmation(ctx, prompt)
    } else {
        description := prompt.Description
        if prompt.Summary != nil {
            description = prompt.Summary.Content(max(20, min(Width(s.options.Err), 96)-4), false, s.Dark())
        }
        answer, err = s.textOwned(ctx, TextPrompt{Title: prompt.Title, Description: description, MaxBytes: 64}, rich && prompt.Kind == ConfirmTypedYes)
    }
    if errors.Is(err, io.EOF) {
        return false, nil
    }
    if err != nil {
        return false, err
    }
    answer = strings.TrimSpace(answer)
    if prompt.Kind == ConfirmTypedYes {
        return answer == "yes", nil
    }
    switch strings.ToLower(answer) {
    case "y", "yes", "是":
        return true, nil
    default:
        return false, nil
    }
}

func (s *InputSession) Secret(ctx context.Context, prompt SecretPrompt) ([]byte, error) {
    if err := s.acquire(ctx, true); err != nil {
        return nil, err
    }
    defer s.release()
    limit := byteLimit(prompt.MaxBytes)
    file, terminal := s.options.In.(*os.File)
    if s.options.ReadPassword == nil && (!terminal || !term.IsTerminal(int(file.Fd()))) {
        return nil, ErrTerminalRequired
    }
    if err := s.printPrompt(prompt.Title, ""); err != nil {
        return nil, err
    }
    var value []byte
    var err error
    if s.options.ReadPassword != nil {
        value, err = s.options.ReadPassword(ctx)
    } else {
        value, err = ReadTerminalSecret(ctx, file, limit)
    }
    newlineErr := writeText(s.options.Err, "\n")
    if err == nil {
        err = ctx.Err()
    }
    if err == nil {
        err = newlineErr
    }
    if err == nil && len(value) > limit {
        err = ErrInputTooLong
    }
    if err == nil && len(value) == 0 {
        err = ErrEmptySecret
    }
    if err != nil {
        clear(value)
        return nil, err
    }
    return value, nil
}

// ReadSecretLine is explicit data input, not a prompt. It is allowed in JSON and
// batch mode and consumes one bounded line, preserving all non-terminator bytes.
func (s *InputSession) ReadSecretLine(ctx context.Context, maxBytes int) ([]byte, error) {
    if err := s.acquire(ctx, false); err != nil {
        return nil, err
    }
    defer s.release()
    value, err := readLine(ctx, s.options.In, byteLimit(maxBytes))
    if err == nil && len(value) == 0 {
        err = ErrEmptySecret
    }
    if err != nil {
        clear(value)
        return nil, err
    }
    return value, nil
}

func (s *InputSession) printPrompt(title, description string) error {
    if description != "" {
        if err := writeText(s.options.Err, description+"\n"); err != nil {
            return err
        }
    }
    return writeText(s.options.Err, title)
}

func writeText(w io.Writer, text string) error {
    n, err := io.WriteString(w, text)
    if err == nil && n != len(text) {
        return io.ErrShortWrite
    }
    return err
}

func byteLimit(limit int) int {
    if limit <= 0 {
        return 4096
    }
    return limit
}
