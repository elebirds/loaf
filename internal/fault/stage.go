package fault

import "errors"

type stageError struct {
    stage string
    cause error
}

func (e *stageError) Error() string { return e.cause.Error() }
func (e *stageError) Unwrap() error { return e.cause }

// At attaches the most specific operation stage without replacing an error's
// code, diagnostics, or identity. Outer callers cannot obscure an inner stage.
func At(err error, stage string) error {
    if err == nil || Stage(err) != "" {
        return err
    }
    return &stageError{stage: stage, cause: err}
}

// Stage returns the stable, non-localized stage associated with a failure.
func Stage(err error) string {
    var staged *stageError
    if errors.As(err, &staged) {
        return staged.stage
    }
    return ""
}
