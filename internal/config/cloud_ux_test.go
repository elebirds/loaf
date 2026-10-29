package config

import (
    "context"
    "reflect"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

func TestCloudConveniencePreferencesNeverRedirectFrozenBindings(t *testing.T) {
    s := testStore(t)
    ctx := context.Background()
    a := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Env: "TEST_TOKEN"}}
    if err := s.AddCloudAccount(ctx, "main", a, false, true); err != nil {
        t.Fatal(err)
    }
    binding := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Account: "main", Product: "standard", ID: "original"}, Settings: a}
    if err := s.Add(ctx, "gpu", Target{Cloud: &binding, RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    pro := "pro"
    if err := s.UseCloudAccount(ctx, "main", &pro); err != nil {
        t.Fatal(err)
    }
    zero, no, cap := 0, false, "0"
    p := cloud.Profile{GPUModels: []string{}, Regions: []string{}, Continuous: &no, MinCPUs: &zero, MaxStorageDailyPrice: &cap}
    if err := s.SaveCloudProfile(ctx, "gpu", p, false); err != nil {
        t.Fatal(err)
    }
    if err := s.SaveCloudProfile(ctx, "gpu", p, false); err == nil {
        t.Fatal("silently replaced profile")
    }
    got, err := s.Load()
    if err != nil || got.Cloud.Accounts["main"].DefaultProduct != "pro" || *got.Targets["gpu"].Cloud != binding || !reflect.DeepEqual(got.Resources["gpu"], p) {
        t.Fatal(got, err)
    }
}
