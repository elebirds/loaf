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
