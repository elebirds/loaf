package client

import (
    "context"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/state/receipts"
)

func TestCloudRequestsRetainsReadableRecoveryIDs(t *testing.T) {
    ctx := context.Background()
    store := cloud.Receipts{Dir: filepath.Join(t.TempDir(), "state-v3")}
    now := time.Now().UTC()
    good, err := store.New(cloud.Receipt{
        Operation: "acquire", Account: "demo", Status: "uncertain",
        Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "TEST_TOKEN"}},
        Plan:     cloud.Plan{Product: "pro"}, CreatedAt: now, Deadline: now.Add(time.Minute),
    })
    if err != nil {
        t.Fatal(err)
    }
    if err := store.Save(good); err != nil {
        t.Fatal(err)
    }
    sqlStore := receipts.Store{Path: filepath.Join(store.Dir, "state.db")}
    if err := sqlStore.SaveEvent(ctx, "c_000000000000000000000000", now.Add(-time.Hour).Format(time.RFC3339Nano), []byte(`{"format_version":999}`), "", nil); err != nil {
        t.Fatal(err)
    }
    resources := Resources{Cloud: &cloud.Service{Receipts: store}}
    result, err := resources.CloudRequests(ctx, CloudSelect{})
    if err == nil || len(result.Requests) != 1 || result.Requests[0].ID != good.ID {
        t.Fatalf("partial recovery inventory: %+v, %v", result, err)
    }
}
