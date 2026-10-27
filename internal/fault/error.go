// Package fault defines language-independent errors for CLI rendering.
package fault

import "fmt"

// Error carries a stable code, message identifier, and safe template parameters.
type Error struct {
    Code    string
    Message string
    Hint    string
    Params  map[string]any
    Cause   error
    Input   *Input
}

// Location identifies an input field without retaining its contents.
type Location struct {
    File   string `json:"file"`
    Field  string `json:"field,omitempty"`
    Line   int    `json:"line,omitempty"`
    Column int    `json:"column,omitempty"`
}

// Input is safe, language-independent context for correcting an invocation.
type Input struct {
    Reason      string
    Argument    string
    HelpCommand []string
    Location    *Location
}

func (e *Error) Error() string {
    if e.Cause != nil {
        return fmt.Sprintf("%s: %v", e.Code, e.Cause)
    }
    return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// New creates a user-facing error without localizing it.
func New(code, message string, params map[string]any) *Error {
    return &Error{Code: code, Message: message, Params: params}
}

// Wrap associates an underlying error with a stable user-facing failure.
func Wrap(code, message string, cause error) *Error {
    return &Error{Code: code, Message: message, Cause: cause}
}

// Invalid reports a specific input problem while retaining the existing code.
func Invalid(argument, reason, message string) *Error {
    e := New("BAD_CONFIG", message, nil)
    e.Input = &Input{Reason: reason, Argument: argument}
    return e
}
