package client

import (
    "errors"
    "testing"

    "github.com/elebirds/loaf/internal/fault"
)

func TestQueueDisabledErrorRetainsIdentityAndCause(t *testing.T) {
    cause := errors.New("test remote error")
    err := queueFailure("QUEUE_NOT_ENABLED", cause)
    var detail *fault.Error
    if !errors.As(err, &detail) || detail.Code != "QUEUE_NOT_ENABLED" || detail.Message != "queue_not_enabled" || fault.Stage(err) != "queue" || !errors.Is(err, cause) {
        t.Fatal(err)
    }
    if detail.Params["Code"] != "QUEUE_NOT_ENABLED" {
        t.Fatal(detail.Params)
    }
}
