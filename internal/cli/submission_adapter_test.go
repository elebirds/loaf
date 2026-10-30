package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "os"
    "testing"
)

func TestUXCoreSyncCancellationKeepsDirectContractBeforeInputOwnership(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{{"sync", "--dry-run", "--json"}, {"sync", "--target", "lab", "--json"}} {
            o := fixture(t)
            sender := &fakeSyncer{}
            o.Syncer, o.In = sender, uxCoreNoInput{t}
            out, diagnostic := &bytes.Buffer{}, &bytes.Buffer{}
            o.Out, o.Err = out, diagnostic
            ctx, cancel := context.WithCancel(context.Background())
            cancel()
            code := execute(ctx, append([]string{"--lang", language}, args...), o)
            var value envelope
            if err := json.Unmarshal(out.Bytes(), &value); err != nil || code != 1 || value.Error == nil || value.Error.Code != "SYNC_CANCELED" || diagnostic.Len() != 0 || sender.Calls != 0 {
                t.Fatal(args, code, out, diagnostic, sender, err)
            }
            if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
                t.Fatal("canceled sync created state", err)
            }
        }
    }
}

// Plain menus write their complete choices before reading. Resolve the actual
// displayed label rather than freezing integration-sensitive menu numbers.
