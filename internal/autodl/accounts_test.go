package autodl

import (
    "context"
    "errors"
    "net/http"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

func TestCredentialModeSelectsHostIndependentlyOfDefaultProduct(t *testing.T) {
    for _, mode := range []string{"session", "developer"} {
        selected, preference, host := "standard", "pro", "www.autodl.com"
        if mode == "developer" {
            selected, preference, host = "pro", "standard", "api.autodl.com"
        }
        calls := 0
        f := factory(t, func(r *http.Request) *http.Response {
            calls++
            if r.URL.Host != host {
                t.Fatal("credential sent to the wrong control plane", r.URL)
            }
            return response(200, `{"code":"Success","data":{"list":[],"result_total":0}}`)
        })
        a := account(selected)
        a.DefaultProduct = preference
        p, err := f.Open(context.Background(), "demo", a, selected)
        if err != nil {
            t.Fatal(err)
        }
        if _, err := p.List(context.Background(), 10); err != nil || calls != 1 {
            t.Fatal(err, calls)
        }
    }
}

func TestUnsupportedCredentialProductNeverReadsOrSendsSecrets(t *testing.T) {
    for _, tc := range []struct{ mode, product string }{{"developer", "standard"}} {
        f := Factory{LookupEnv: func(string) (string, bool) {
            t.Fatal("read a secret before rejecting an unsupported control plane")
            return "", false
        }}
        a := account("standard")
        a.Credential.Mode = tc.mode
        _, err := f.Open(context.Background(), "demo", a, tc.product)
        var api *cloud.APIError
        if !errors.As(err, &api) || api.Code != "UNSUPPORTED_AUTH_PRODUCT" {
            t.Fatal(tc, err)
        }
    }
}

func TestCredentialStoreIsExplicitAndErrorsAreRedacted(t *testing.T) {
    a := account("standard")
    a.Credential.Env = ""
    a.Credential.Key = "session-123"
    a.Credential.Store = "keyring"
    a.Credential.Identity = cloud.Identity{Kind: "sub", ID: "123", Username: "student@master"}
    f := factory(t, func(r *http.Request) *http.Response {
        if r.URL.Path == "/api/v1/sub_user" {
            return response(200, `{"code":"Success","data":{"uuid":"parent-123","id":123,"sub_name":"student@master","roles":{"instance":"all","instance_pro":"all"}}}`)
        }
        if r.URL.Path != "/api/v1/sub_user/instance" {
            t.Fatal(r.URL)
        }
        return response(200, `{"code":"Success","data":{"list":[],"result_total":0}}`)
    })
    f.LookupEnv = func(string) (string, bool) {
        t.Fatal("silently fell back to an environment variable")
        return "", false
    }
    reads := 0
    f.ReadSecret = func(ctx context.Context, ref cloud.CredentialRef) (string, error) {
        reads++
        if ref != a.Credential || ctx.Err() != nil {
            t.Fatal(ref, ctx.Err())
        }
        return "fixture-token", nil
    }
    p, err := f.Open(context.Background(), "demo", a, "standard")
    if err != nil || reads != 1 {
        t.Fatal(err, reads)
    }
    if _, err := p.List(context.Background(), 10); err != nil {
        t.Fatal(err)
    }
    for _, reader := range []func(context.Context, cloud.CredentialRef) (string, error){nil, func(context.Context, cloud.CredentialRef) (string, error) {
        return "", errors.New("fixture-token private diagnostic")
    }} {
        f.ReadSecret = reader
        _, err := f.Open(context.Background(), "demo", a, "standard")
        var api *cloud.APIError
        if !errors.As(err, &api) || api.Code != "CREDENTIAL_STORE_UNAVAILABLE" || strings.Contains(err.Error(), "fixture-token") {
            t.Fatal(err)
        }
    }
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    _, err = f.Open(ctx, "demo", a, "standard")
    if !errors.Is(err, context.Canceled) {
        t.Fatal("credential resolution ignored cancellation", err)
    }
}

func TestSubaccountNamespaceKeepsOnlyKnownCatalogExceptions(t *testing.T) {
    a := &api{settings: account("standard")}
    a.settings.Credential.Identity = cloud.Identity{Kind: "sub", ID: "123", Username: "student@master"}
    for input, want := range map[string]string{
        "/api/v1":                                  "/api/v1/sub_user",
        "/api/v1/passport":                         "/api/v1/sub_user/passport",
        "/api/v1/instance":                         "/api/v1/sub_user/instance",
        "/api/v1/user/machine/list":                "/api/v1/sub_user/user/machine/list",
        "/api/v1/order/instance/create/payg":       "/api/v1/sub_user/order/instance/create/payg",
        "/api/v1/instance/pro":                     "/api/v1/sub_user/instance/pro",
        "/api/v1/instance/pro/snapshot":            "/api/v1/sub_user/instance/pro/snapshot",
        "/api/v1/instance/pro/get/gpu_spec_config": "/api/v1/sub_user/instance/pro/get/gpu_spec_config",
        "/api/v1/order/price/preview/pro":          "/api/v1/sub_user/order/price/preview/pro",
        "/api/v1/machine_pro/gpu_type":             "/api/v1/machine_pro/gpu_type",
        "/api/v1/machine_pro/gpu_spec_config":      "/api/v1/machine_pro/gpu_spec_config",
        "/api/v1/machine_pro/get/gpu_spec_config":  "/api/v1/machine_pro/get/gpu_spec_config",
    } {
        if got := a.endpoint(input); got != want {
            t.Fatal(input, got, want)
        }
    }
    a.settings.Credential.Mode = "developer"
    if got := a.endpoint("/api/v1/dev/instance/pro/list"); got != "/api/v1/dev/instance/pro/list" {
        t.Fatal("rewrote a developer control-plane endpoint", got)
    }
}
