package config

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

func TestCloudAccountsDoNotPersistTokensOrRedirectBindings(t *testing.T) {
    s := testStore(t)
    ctx := context.Background()
    a := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Env: "TEST_TOKEN"}}
    if err := s.AddCloudAccount(ctx, "demo", a, false, true); err != nil {
        t.Fatal(err)
    }
    b := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "demo", ID: "saved"}, Settings: a}
    if err := s.Add(ctx, "gpu", Target{Cloud: &b, RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    changed := cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "OTHER_TOKEN"}}
    if err := s.AddCloudAccount(ctx, "demo", changed, true, false); err != nil {
        t.Fatal(err)
    }
    if err := s.RemoveCloudAccount(ctx, "demo"); err != nil {
        t.Fatal(err)
    }
    cfg, err := s.Load()
    if err != nil || cfg.Cloud.DefaultAccount != "" || *cfg.Targets["gpu"].Cloud != b {
        t.Fatal(cfg, err)
    }
    mode, _ := os.Stat(s.ConfigFile)
    if mode.Mode().Perm() != 0600 {
        t.Fatal(mode.Mode())
    }
}

func TestProjectAcceptsResourceRequirementsButNeverAccounts(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "loaf.toml")
    if err := os.WriteFile(path, []byte("[resources.train]\ngpu_count = 2\nmin_gpu_memory_gib = 24\n[resources.train.autodl]\nimage = 'explicit'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    _, file, err := Discover(context.Background(), dir)
    if err != nil || *file.Resources["train"].GPUCount != 2 || *file.Resources["train"].AutoDL.Image != "explicit" {
        t.Fatal(file, err)
    }
    for _, body := range []string{"[cloud.accounts.demo]\ntoken_env = 'TOKEN'\n", "[resources.train]\ntoken = 'secret'\n"} {
        if err := os.WriteFile(path, []byte(body), 0600); err != nil {
            t.Fatal(err)
        }
        if _, _, err := Discover(context.Background(), dir); err == nil {
            t.Fatal("accepted shared credential settings")
        }
    }
}

func TestStoredIdentityAndProductBindingAreIndependent(t *testing.T) {
    s := testStore(t)
    identity := cloud.Identity{Kind: "sub", ID: "sub-123", Username: "student@master"}
    a := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Key: "session-123", Store: "keyring", Identity: identity}}
    ctx := context.Background()
    if err := s.AddCloudAccount(ctx, "lab", a, false, true); err != nil {
        t.Fatal(err)
    }
    binding := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "pro", Account: "lab", ID: "pro-saved"}, Settings: a}
    if !ValidCloudBinding(binding) {
        t.Fatal("default product was treated as the bound resource product")
    }
    if err := s.Add(ctx, "gpu", Target{Cloud: &binding, RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    cfg, err := s.Load()
    if err != nil || cfg.Cloud.Accounts["lab"] != a || *cfg.Targets["gpu"].Cloud != binding {
        t.Fatal(cfg, err)
    }
    changed := a
    changed.Credential.Key = "another-session"
    changed.Credential.Identity = cloud.Identity{Kind: "main", ID: "other-user", Username: "other"}
    changed.DefaultProduct = "pro"
    if err := s.AddCloudAccount(ctx, "lab", changed, true, true); err != nil {
        t.Fatal(err)
    }
    cfg, err = s.Load()
    if err != nil || cfg.Targets["gpu"].Cloud.Settings.Credential.Identity != identity || cfg.Targets["gpu"].Cloud.Settings.Credential.Key != "session-123" {
        t.Fatal("account replacement rewrote historical access", cfg, err)
    }
}
