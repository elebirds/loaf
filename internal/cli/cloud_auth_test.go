package cli

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/credentials"
)

func authFixture(t *testing.T) (testOptions, *int) {
    t.Helper()
    o, _ := cloudFixture(t)
    calls := new(int)
    o.Auth = &cloud.Authentication{
        Keyring: credentials.File{Dir: filepath.Join(t.TempDir(), "vault-fixture")},
        Files:   credentials.File{Dir: filepath.Join(t.TempDir(), "file-fixture")},
        Authenticate: func(_ context.Context, in cloud.LoginInput) (cloud.Session, error) {
            *calls++
            if in.Username != "student@master" || string(in.Password) != " raw password " && in.Digest != strings.Repeat("a", 40) {
                t.Fatal("wrong input credentials")
            }
            yes := true
            return cloud.Session{Identity: cloud.Identity{Kind: "sub", ID: "verified-sub", Username: in.Username}, Username: in.Username, Token: "login-token-private", Digest: strings.Repeat("a", 40), DeviceID: "device-private", PhoneArea: in.PhoneArea, Standard: true, Pro: &yes}, nil
        },
    }
    return o, calls
}

func TestCloudLoginStoreSelectionRememberedReloginAndLogout(t *testing.T) {
    o, calls := authFixture(t)
    o.In = strings.NewReader(" raw password \n")
    code, out, stderr := run(t, o, "cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--credential-store", "file", "--default", "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, `"credential_store":"file"`) {
        t.Fatal(code, out, stderr)
    }
    cfg, err := o.Config.Load()
    if err != nil {
        t.Fatal(err)
    }
    saved := cfg.Cloud.Accounts["lab"]
    if saved.Credential.Store != "file" || saved.Credential.Identity.ID != "verified-sub" || cfg.Cloud.DefaultAccount != "lab" {
        t.Fatal(cfg)
    }
    settings, _ := os.ReadFile(o.Config.ConfigFile)
    for _, secret := range []string{" raw password ", "login-token-private", strings.Repeat("a", 40), "device-private"} {
        if strings.Contains(string(settings)+out+stderr, secret) {
            t.Fatal("credentials escaped into public files/output")
        }
    }
    o.In = strings.NewReader("")
    code, out, stderr = run(t, o, "cloud", "login", "lab", "--remember-password=false", "--json")
    if code != 0 || stderr != "" || *calls != 2 {
        t.Fatal(code, out, stderr, *calls)
    }
    cfg, _ = o.Config.Load()
    if cfg.Cloud.Accounts["lab"].Credential != saved.Credential {
        t.Fatal("re-login changed the bound identity")
    }
    session, err := o.Auth.Read(context.Background(), saved.Credential)
    if err != nil || session.Digest != "" {
        t.Fatal("retained digest", err)
    }
    code, out, stderr = run(t, o, "cloud", "login", "lab", "--json")
    if code != 2 || stderr != "" || *calls != 2 {
        t.Fatal("noninteractive login prompted/replayed without a saved digest", code, out, stderr)
    }
    code, out, stderr = run(t, o, "cloud", "logout", "lab", "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, `"logged_out"`) {
        t.Fatal(code, out, stderr)
    }
    if _, err := o.Auth.Read(context.Background(), saved.Credential); err == nil {
        t.Fatal("logout retained selected secret")
    }
    cfg, _ = o.Config.Load()
    if cfg.Cloud.Accounts["lab"] != saved {
        t.Fatal("logout removed the reference needed by saved runs")
    }
}

func TestCloudLoginDefaultGlobalAndExplicitStores(t *testing.T) {
    for _, choice := range []string{"keyring", "file"} {
        t.Run(choice, func(t *testing.T) {
            o, _ := authFixture(t)
            body, _ := os.ReadFile(o.Config.ConfigFile)
            body = []byte(strings.Replace(string(body), "[cloud]", "[cloud]\ncredential_store = '"+choice+"'", 1))
            if err := os.WriteFile(o.Config.ConfigFile, body, 0600); err != nil {
                t.Fatal(err)
            }
            o.In = strings.NewReader(" raw password \n")
            code, out, stderr := run(t, o, "cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--json")
            if code != 0 || stderr != "" || !strings.Contains(out, `"credential_store":"`+choice+`"`) {
                t.Fatal(code, out, stderr)
            }
            other := "file"
            if choice == "file" {
                other = "keyring"
            }
            code, out, stderr = run(t, o, "cloud", "login", "lab", "--credential-store", other, "--json")
            if code != 0 || stderr != "" || !strings.Contains(out, `"credential_store":"`+other+`"`) {
                t.Fatal(code, out, stderr)
            }
        })
    }
    o, _ := authFixture(t)
    o.In = strings.NewReader(" raw password \n")
    code, out, stderr := run(t, o, "cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--json")
    if code != 0 || stderr != "" || !strings.Contains(out, `"credential_store":"keyring"`) {
        t.Fatal(code, out, stderr)
    }
}

func TestCloudLoginAfterLogoutRequestsFreshCredentials(t *testing.T) {
    for _, structured := range []bool{false, true} {
        t.Run(map[bool]string{false: "interactive", true: "json"}[structured], func(t *testing.T) {
            o, calls := authFixture(t)
            o.In = strings.NewReader(" raw password \n")
            args := []string{"cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--credential-store", "file", "--json"}
            if code, out, stderr := run(t, o, args...); code != 0 {
                t.Fatal(code, out, stderr)
            }
            cfg, _ := o.Config.Load()
            original := cfg.Cloud.Accounts["lab"].Credential
            if code, out, stderr := run(t, o, "cloud", "logout", "lab", "--json"); code != 0 {
                t.Fatal(code, out, stderr)
            }
            o.Interactive = true
            o.In = strings.NewReader("student@master\n")
            o.ReadPassword = func(context.Context) ([]byte, error) {
                if structured {
                    t.Fatal("JSON login prompted")
                }
                return []byte(" raw password "), nil
            }
            args = []string{"cloud", "login", "lab"}
            if structured {
                args = append(args, "--username", "student@master", "--json")
            }
            code, out, stderr := run(t, o, args...)
            if structured {
                if code != 2 || stderr != "" || *calls != 1 {
                    t.Fatal("JSON must require explicit password input", code, out, stderr, *calls)
                }
            } else {
                cfg, _ = o.Config.Load()
                if code != 0 || *calls != 2 || cfg.Cloud.Accounts["lab"].Credential != original {
                    t.Fatal("cannot log in again after logout", code, out, stderr, *calls)
                }
            }
        })
    }
}

func TestCloudLoginErrorsAndBothHelpCatalogs(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        o, calls := authFixture(t)
        code, out, stderr := run(t, o, "--lang", lang, "cloud", "login", "--help")
        if code != 0 || stderr != "" || !strings.Contains(out, "--credential-store") || !strings.Contains(out, "--remember-password") {
            t.Fatal(code, out, stderr)
        }
        o.In = strings.NewReader(" raw password \n")
        code, out, stderr = run(t, o, "--lang", lang, "cloud", "login", "lab", "--credential-store", "automatic", "--password-stdin", "--username", "student@master", "--json")
        if code != 2 || *calls != 0 || stderr != "" {
            t.Fatal(code, out, stderr)
        }
        o.Auth.Authenticate = func(context.Context, cloud.LoginInput) (cloud.Session, error) {
            return cloud.Session{}, &cloud.APIError{Code: "AUTH_CHALLENGE_REQUIRED"}
        }
        o.In = strings.NewReader(" raw password \n")
        code, out, stderr = run(t, o, "--lang", lang, "cloud", "login", "lab", "--password-stdin", "--username", "student@master", "--json")
        if code != 1 || stderr != "" || !strings.Contains(out, "AUTH_CHALLENGE_REQUIRED") {
            t.Fatal(code, out, stderr)
        }
    }
    // Terminal input is cleared after use, and never read for JSON mode.
    o, _ := authFixture(t)
    o.Interactive = true
    password := []byte(" raw password ")
    o.ReadPassword = func(context.Context) ([]byte, error) { return password, nil }
    code, out, stderr := run(t, o, "cloud", "login", "lab", "--username", "student@master", "--credential-store", "file")
    if code != 0 || !strings.Contains(stderr, ":") {
        t.Fatal(code, out, stderr)
    }
    for _, b := range password {
        if b != 0 {
            t.Fatal("kept terminal password buffer")
        }
    }
    o.ReadPassword = func(context.Context) ([]byte, error) { t.Fatal("JSON prompted"); return nil, errors.New("unreachable") }
    code, out, stderr = run(t, o, "cloud", "login", "other", "--username", "student@master", "--json")
    if code != 2 || stderr != "" {
        t.Fatal(code, out, stderr)
    }
}

func TestLoginDiagnosticIsVisibleInBothLanguagesAndStructuredOutput(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, structured := range []bool{false, true} {
            o, _ := authFixture(t)
            o.Auth.Authenticate = func(context.Context, cloud.LoginInput) (cloud.Session, error) {
                return cloud.Session{}, &cloud.APIError{Code: "INVALID_RESPONSE", HTTPStatus: 200, AuthStage: "identity"}
            }
            o.In = strings.NewReader(" raw password \n")
            args := []string{"--lang", lang, "cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--credential-store", "file"}
            if structured {
                args = append(args, "--json")
            }
            code, out, stderr := run(t, o, args...)
            if code != 1 || !strings.Contains(out+stderr, "INVALID_RESPONSE") || strings.Contains(out+stderr, "replacement resource") || strings.Contains(out+stderr, " raw password ") {
                t.Fatal(code, out, stderr)
            }
            if structured && (!strings.Contains(out, `"auth_stage":"identity"`) || stderr != "") {
                t.Fatal(out, stderr)
            }
            if !structured && lang == "zh-Hans" && !strings.Contains(stderr, "身份及权限核验") {
                t.Fatal(stderr)
            }
        }
    }
}

func TestCloudLoginInvalidInputNeverAuthenticates(t *testing.T) {
    for _, input := range []string{"\n", strings.Repeat("x", 4097) + "\n"} {
        for _, structured := range []bool{false, true} {
            o, calls := authFixture(t)
            o.In = strings.NewReader(input)
            o.ReadPassword = func(context.Context) ([]byte, error) { t.Fatal("stdin password prompted"); return nil, nil }
            args := []string{"cloud", "login", "lab", "--username", "student@master", "--password-stdin"}
            if structured {
                args = append(args, "--json")
            }
            code, out, stderr := run(t, o, args...)
            if code != 2 || *calls != 0 || !structured && out != "" || structured && (stderr != "" || !strings.Contains(out, `"code":"BAD_CONFIG"`) || strings.Contains(out, `"account":""`)) {
                t.Fatal("invalid stdin credentials did not fail before authentication", code, out, stderr, *calls)
            }
        }
    }
    o, calls := authFixture(t)
    o.Interactive = true
    o.ReadPassword = func(context.Context) ([]byte, error) { return []byte{}, nil }
    code, out, stderr := run(t, o, "cloud", "login", "lab", "--username", "student@master")
    if code != 2 || *calls != 0 || out != "" {
        t.Fatal("empty terminal password was not a parameter error", code, out, stderr)
    }

    o, calls = authFixture(t)
    o.Interactive = true
    o.In = strings.NewReader("")
    o.ReadPassword = func(context.Context) ([]byte, error) {
        t.Fatal("missing username prompted for a password")
        return nil, nil
    }
    code, out, stderr = run(t, o, "cloud", "login", "lab")
    if code != 2 || *calls != 0 || out != "" {
        t.Fatal("missing username was not a parameter error", code, out, stderr)
    }
}

func TestCloudLoginBatchDoesNotPromptAndFailedSecretIsCleared(t *testing.T) {
    o, calls := authFixture(t)
    o.ReadPassword = func(context.Context) ([]byte, error) { t.Fatal("batch login prompted"); return nil, nil }
    input := strings.NewReader("do not consume\n")
    o.In = input
    code, out, stderr := run(t, o, "cloud", "login", "lab", "--username", "student@master")
    if code != 2 || *calls != 0 || input.Len() != len("do not consume\n") {
        t.Fatal(code, out, stderr, *calls)
    }

    o.Interactive = true
    password := []byte(" private failed input ")
    o.ReadPassword = func(context.Context) ([]byte, error) { return password, context.Canceled }
    code, out, stderr = run(t, o, "cloud", "login", "lab", "--username", "student@master")
    if code != 1 || *calls != 0 || out != "" || strings.Contains(out+stderr, "private failed input") {
        t.Fatal(code, out, stderr, *calls)
    }
    for _, b := range password {
        if b != 0 {
            t.Fatal("failed password buffer retained")
        }
    }
}
