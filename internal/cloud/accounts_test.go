package cloud

import "testing"

func TestCredentialReferencesSeparateIdentityModeAndLocation(t *testing.T) {
    identity := Identity{Kind: "sub", ID: "sub-123", Username: "student@master-456"}
    for _, ref := range []CredentialRef{
        {Mode: "session", Env: "WEB_TOKEN"},
        {Mode: "developer", Env: "DEV_TOKEN"},
        {Mode: "session", Env: "SUB_TOKEN", Identity: identity},
        {Mode: "session", Key: "session-123", Store: "keyring", Identity: identity},
        {Mode: "session", Key: "session-123", Store: "file", Identity: identity},
        {Mode: "session", Key: "session-456", Store: "keyring", Identity: Identity{Kind: "main", ID: "user-1", Username: "user"}},
    } {
        for _, product := range []string{"standard", "pro"} {
            a := Account{Provider: "autodl", DefaultProduct: product, Credential: ref}
            if err := a.Validate(); err != nil {
                t.Fatal("a default product must not reinterpret a credential", a, err)
            }
        }
    }
    for _, ref := range []CredentialRef{
        {},
        {Mode: "automatic", Env: "TOKEN"},
        {Mode: "session"},
        {Mode: "session", Env: "TOKEN", Key: "session-1", Store: "keyring", Identity: identity},
        {Mode: "session", Env: "not an env name"},
        {Mode: "session", Key: "../session", Store: "keyring", Identity: identity},
        {Mode: "session", Key: "session-1"},
        {Mode: "session", Key: "session-1", Identity: identity},
        {Mode: "session", Env: "TOKEN", Store: "file"},
        {Mode: "session", Key: "session-1", Store: "automatic", Identity: identity},
        {Mode: "developer", Env: "TOKEN", Identity: identity},
        {Mode: "session", Env: "TOKEN", Identity: Identity{Kind: "sub"}},
        {Mode: "session", Env: "TOKEN", Identity: Identity{Kind: "admin", ID: "1", Username: "user"}},
        {Mode: "session", Env: "TOKEN", Identity: Identity{Kind: "sub", ID: "1", Username: "user\nother"}},
    } {
        if err := ref.Validate(); err == nil {
            t.Fatal("accepted an ambiguous or invalid credential reference", ref)
        }
    }
}

func TestProductResolutionPreservesExplicitSelection(t *testing.T) {
    p, err := Resolve(Ceiling{}, Profile{Product: ptr("standard")}, Profile{Product: ptr("pro")})
    if err != nil || p.Product != "pro" {
        t.Fatal(p, err)
    }
    unset, err := Resolve(Ceiling{})
    if err != nil || unset.Product != "" {
        t.Fatal("unset product must remain available for account-default resolution", unset, err)
    }
    for _, value := range []string{"", "auto", "elastic"} {
        if _, err := Resolve(Ceiling{}, Profile{Product: &value}); err == nil {
            t.Fatal("accepted unsupported explicit product", value)
        }
    }
}

func TestProWebAndDeveloperHaveDifferentCardLimits(t *testing.T) {
    plan := Plan{Product: "pro", Requirements: Requirements{GPUCount: 8}, AutoDL: ImageOptions{Image: "base", CUDAFrom: 118}}
    if err := validateCreate(plan, "session"); err != nil {
        t.Fatal("inherited developer API limit", err)
    }
    plan.AutoDL.GPUSpecUUID = "spec"
    if err := validateCreate(plan, "developer"); err == nil {
        t.Fatal("relaxed developer API limit")
    }
}
