package client

import (
    "context"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
)

func TestClonePreviewResolvesFrozenSourceLocally(t *testing.T) {
    a := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Env: "FIXTURE_TOKEN"}}
    b := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "removed", ID: "original"}, Settings: a}
    settings := config.Global{Targets: map[string]config.Target{"old": {Cloud: &b, RemoteBase: "~/projects"}}}
    r := Resources{Settings: settings, CWD: t.TempDir()}
    name := "old"
    input := CloudAcquireInput{Name: "copy", CloneFrom: &name, RemoteBase: "~/projects", Limit: 100, ReadyTimeout: time.Minute}
    got, err := r.CloudPreview(context.Background(), input)
    if err != nil || got.CloneSource == nil || *got.CloneSource != b.Ref || got.Account != "removed" || got.Plan.WaitTimeout != "30m" || len(got.Inherited) != 4 {
        t.Fatal(got, err)
    }
    image := "other-image"
    input.Plan.Explicit.AutoDL.Image = &image
    if _, err := r.CloudPreview(context.Background(), input); err == nil {
        t.Fatal("clone preview accepted conflicting image")
    }
}

func TestCatalogUnsupportedBeforeSecretAccess(t *testing.T) {
    a := cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "FIXTURE_TOKEN"}}
    r := Resources{Settings: config.Global{Cloud: cloud.Settings{DefaultAccount: "main", Accounts: map[string]cloud.Account{"main": a}}}, Cloud: &cloud.Service{Open: func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        t.Fatal("opened unsupported directory credentials")
        return nil, nil
    }}}
    if _, err := r.CloudCatalog(context.Background(), CloudSelect{}, cloud.CatalogInput{Category: "image", Limit: 100}); err == nil {
        t.Fatal("developer directory invented")
    }
}

func TestPreviewRejectsExistingLocalAliasWithoutState(t *testing.T) {
    r := Resources{Config: config.New(filepath.Join(t.TempDir(), "home"), "", ""), Settings: config.Global{Targets: map[string]config.Target{"gpu": {Destination: "fixture"}}}, CWD: t.TempDir()}
    _, err := r.CloudPreview(context.Background(), CloudAcquireInput{Name: "gpu", RemoteBase: "~/projects", Limit: 100})
    if err == nil {
        t.Fatal("preview accepted occupied alias")
    }
}

func TestCloudAliasesFollowPrincipalNotConvenienceDefaults(t *testing.T) {
    a := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Env: "FIRST_TOKEN"}}
    b := a
    b.DefaultProduct = "pro"
    b.AppVersion = "changed"
    if !sameCloudPrincipal(a, b) {
        t.Fatal("preference hid the alias")
    }
    b.Credential.Env = "OTHER_TOKEN"
    if sameCloudPrincipal(a, b) {
        t.Fatal("unverified replacement inherited alias")
    }
    a.Credential.Identity = cloud.Identity{Kind: "main", ID: "same-user", Username: "before"}
    b.Credential.Identity = cloud.Identity{Kind: "main", ID: "same-user", Username: "after"}
    if !sameCloudPrincipal(a, b) {
        t.Fatal("reauthentication hid the same principal")
    }
    b.Credential.Identity.ID = "different-user"
    if sameCloudPrincipal(a, b) {
        t.Fatal("different principal inherited alias")
    }
}
