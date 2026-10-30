package client

import (
    "context"
    "errors"
    "strings"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
)

// Login authenticates and publishes the selected credential reference. All input
// callbacks belong to one caller-owned input session. Batch calls may consume an
// explicitly requested stdin password, but never invoke interactive callbacks.
func (r *Resources) Login(ctx context.Context, input LoginInput, policy CallPolicy, prompts LoginPrompts) (LoginResult, error) {
    var result LoginResult
    if err := ctx.Err(); err != nil {
        return result, err
    }
    if !config.ValidName(input.Name) || r.Auth == nil || r.Config == nil || input.Product != nil && !cloud.ValidProduct(*input.Product) {
        return result, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    if input.PasswordStdin && input.Prompt {
        return result, fault.New("BAD_CONFIG", "invalid_flags", nil)
    }
    var previous *cloud.Account
    if old, ok := r.Settings.Cloud.Accounts[input.Name]; ok {
        previous = &old
    }
    source := ""
    if input.CredentialStore != nil {
        source = *input.CredentialStore
    } else {
        if previous != nil && previous.Credential.Key != "" {
            source = previous.Credential.Store
        } else {
            source = r.Settings.Cloud.CredentialStore
        }
        if source == "" {
            source = "keyring"
        }
    }
    if !cloud.ValidCredentialStore(source) {
        return result, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    in := cloud.LoginInput{Username: input.Username, PhoneArea: "+86"}
    if input.PhoneArea != nil {
        in.PhoneArea = *input.PhoneArea
    }
    if previous != nil && previous.Credential.Key != "" && !input.PasswordStdin && !input.Prompt {
        saved, err := r.Auth.Read(ctx, previous.Credential)
        if err != nil {
            var api *cloud.APIError
            if !errors.As(err, &api) || api.Code != "AUTH_REQUIRED" {
                return result, cloud.Failure(err)
            }
        }
        if err == nil && (input.Username == "" || input.Username == saved.Username) {
            in.Username, in.Digest, in.DeviceID = saved.Username, saved.Digest, saved.DeviceID
            if input.PhoneArea == nil {
                in.PhoneArea = saved.PhoneArea
            }
        }
    }
    if in.Username == "" {
        if policy.Batch || prompts.Username == nil {
            return result, fault.New("BAD_CONFIG", "cloud_username_required", nil)
        }
        name, err := prompts.Username(ctx)
        if err != nil {
            return result, err
        }
        in.Username = strings.TrimSpace(name)
    }
    if in.Username == "" || len(in.Username) > 512 || strings.ContainsAny(in.Username, "\x00\r\n") {
        return result, fault.New("BAD_CONFIG", "cloud_username_required", nil)
    }
    if in.Digest == "" {
        readPassword := prompts.Password
        if input.PasswordStdin {
            readPassword = prompts.StdinPassword
        } else if policy.Batch {
            readPassword = nil
        }
        if readPassword == nil {
            return result, fault.New("BAD_CONFIG", "cloud_password_required", nil)
        }
        var err error
        in.Password, err = readPassword(ctx)
        defer clear(in.Password)
        if err != nil {
            return result, err
        }
        if len(in.Password) == 0 || len(in.Password) > 4096 {
            return result, fault.New("BAD_CONFIG", "cloud_password_required", nil)
        }
    }
    if input.Verify {
        if policy.Batch || prompts.Verification == nil {
            return result, fault.New("BAD_CONFIG", "cloud_verification_required", nil)
        }
        code, err := prompts.Verification(ctx)
        if err != nil {
            clear(code)
            return result, err
        }
        in.Code = string(code)
        clear(code)
    }
    if err := ctx.Err(); err != nil {
        return result, err
    }
    _, session, err := r.Auth.SignIn(ctx, in, previous, cloud.LoginOptions{Store: source, Replace: input.Replace, Remember: input.Remember}, func(account cloud.Account) error {
        if input.Product != nil {
            account.DefaultProduct = *input.Product
        }
        return r.Config.SaveLogin(ctx, input.Name, account, previous, input.Default)
    })
    if err != nil {
        return result, cloud.Failure(err)
    }
    result = LoginResult{Account: input.Name, Store: source, Identity: session.Identity, Remembered: input.Remember, Standard: session.Standard, Pro: session.Pro}
    settings, err := r.Config.Load()
    if err != nil {
        return result, err
    }
    r.Settings = settings
    return result, nil
}

// Logout erases the selected credential entry, not aliases or saved run bindings.
func (r *Resources) Logout(ctx context.Context, name string) (LogoutResult, error) {
    var result LogoutResult
    if err := ctx.Err(); err != nil {
        return result, err
    }
    account, ok := r.Settings.Cloud.Accounts[name]
    if !ok || r.Auth == nil {
        return result, fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
    }
    if err := r.Auth.Forget(ctx, account.Credential); err != nil {
        return result, cloud.Failure(err)
    }
    return LogoutResult{Account: name, Status: "logged_out"}, nil
}
