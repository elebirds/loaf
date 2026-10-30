package remote

import (
    "context"
    "os"
    "testing"
)

func TestAbandonMissingPayloadRetryConfirmsRetainedFence(t *testing.T) {
    req, dir := abandonFixture(t)
    receipt, err := Abandon(cancelAfterFence{context.Background(), req.Root, req.RunID}, req, false)
    expectCode(t, err, "ABANDON_UNCONFIRMED")
    if receipt == nil || receipt.NoStartAt == nil {
        t.Fatal(receipt)
    }
    // Model successful removal followed by a lost parent flush/checkpoint.
    if err := os.RemoveAll(dir); err != nil {
        t.Fatal(err)
    }
    done, err := Abandon(context.Background(), req, false)
    if err != nil || done.CleanedAt == nil || !done.NoStartAt.Equal(*receipt.NoStartAt) {
        t.Fatal(done, err)
    }
}
