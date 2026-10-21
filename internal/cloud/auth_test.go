package cloud

import (
    "context"
    "encoding/json"
    "errors"
    "io/fs"
    "strings"
    "testing"
)

type memorySecrets struct {
    values                 map[string]string
    err                    error
    reads, writes, deletes int
}

func (s *memorySecrets) Get(_ context.Context, key string) (string, error) {
    s.reads++
    if s.err != nil {
        return "", s.err
    }
    v, ok := s.values[key]
    if !ok {
        return "", fs.ErrNotExist
    }
    return v, nil
}
func (s *memorySecrets) Set(_ context.Context, key, value string) error {
    s.writes++
    if s.err != nil {
        return s.err
    }
    s.values[key] = value
    return nil
}
func (s *memorySecrets) Delete(_ context.Context, key string) error {
    s.deletes++
    if s.err != nil {
        return s.err
    }
    delete(s.values, key)
    return nil
}
func testAuthentication() (*Authentication, *memorySecrets, *memorySecrets, Session) {
    keyring := &memorySecrets{values: map[string]string{}}
    files := &memorySecrets{values: map[string]string{}}
    yes := true
    session := Session{Identity: Identity{Kind: "sub", ID: "sub-1", Username: "student@master"}, Username: "student@master", PhoneArea: "+86", DeviceID: "device-1", Token: "secret-token", Digest: strings.Repeat("a", 40), Standard: true, Pro: &yes}
    a := &Authentication{Keyring: keyring, Files: files, Authenticate: func(context.Context, LoginInput) (Session, error) { return session, nil }}
    return a, keyring, files, session
}
func apiCode(t *testing.T, err error, want string) {
    t.Helper()
    var api *APIError
    if !errors.As(err, &api) || api.Code != want {
        t.Fatal(err, want)
    }
}

func TestAuthenticationUsesExplicitStoreAndKeepsHistoricalReferences(t *testing.T) {
    a, keyring, files, session := testAuthentication()
    ctx := context.Background()
    original, err := a.Save(ctx, session, nil, "keyring", true)
    if err != nil || len(keyring.values) != 1 || len(files.values) != 0 {
        t.Fatal(err)
    }
    again, err := a.Save(ctx, session, &original, "keyring", false)
    if err != nil || again.Credential != original.Credential {
        t.Fatal(again, err)
    }
    saved, err := a.Read(ctx, again.Credential)
    if err != nil || saved.Digest != "" {
        t.Fatal("no-remember retained digest", err)
    }
    switched, err := a.Save(ctx, session, &again, "file", true)
    if err != nil || switched.Credential.Store != "file" || switched.Credential.Key == original.Credential.Key {
        t.Fatal(switched, err)
    }
    if _, err := a.Token(ctx, original.Credential); err != nil {
        t.Fatal("switch deleted old run access", err)
    }
    if err := a.Forget(ctx, switched.Credential); err != nil || len(files.values) != 0 || len(keyring.values) != 1 {
        t.Fatal(err)
    }
    if _, err := a.Token(ctx, switched.Credential); err == nil {
        t.Fatal("logged-out credential remained")
    }
    fileReads := files.reads
    keyring.err = errors.New("private error: secret-token")
    _, err = a.Token(ctx, original.Credential)
    apiCode(t, err, "CREDENTIAL_STORE_UNAVAILABLE")
    if files.reads != fileReads {
        t.Fatal("silently fell back to file")
    }
    data, _ := json.Marshal([]any{session, original, LoginInput{Password: []byte("raw-password"), Digest: session.Digest, Code: "sms-private", DeviceID: "device-private"}})
    for _, secret := range []string{"secret-token", session.Digest, "raw-password", "sms-private", "device-private"} {
        if strings.Contains(string(data), secret) {
            t.Fatal("public secret leak")
        }
    }
}

func TestAuthenticationDoesNotOverwriteUnknownRecordsOrChangedIdentity(t *testing.T) {
    a, keyring, _, s := testAuthentication()
    ctx := context.Background()
    account, err := a.Save(ctx, s, nil, "keyring", true)
    if err != nil {
        t.Fatal(err)
    }
    original := keyring.values[account.Credential.Key]
    for _, data := range []string{strings.Replace(original, `"format_version":1`, `"format_version":99`, 1), strings.Replace(original, `"sub-1"`, `"other-user"`, 1), original[:len(original)-1] + `,"future":true}`, original + ` {}`, strings.Replace(original, `"password_sha1":"`+s.Digest+`"`, `"password_sha1":"invalid"`, 1)} {
        keyring.values[account.Credential.Key] = data
        _, err := a.Read(ctx, account.Credential)
        apiCode(t, err, "INVALID_CREDENTIAL_RECORD")
        _, err = a.Save(ctx, s, &account, "keyring", true)
        apiCode(t, err, "INVALID_CREDENTIAL_RECORD")
        if keyring.values[account.Credential.Key] != data {
            t.Fatal("overwrote rejected record")
        }
    }
}

func TestSignInPublishesOnlyVerifiedIdentityAndCleansFailedPublication(t *testing.T) {
    a, keyring, files, s := testAuthentication()
    ctx := context.Background()
    old, err := a.Save(ctx, s, nil, "keyring", true)
    if err != nil {
        t.Fatal(err)
    }
    s.Identity.ID = "different-principal"
    a.Authenticate = func(context.Context, LoginInput) (Session, error) { return s, nil }
    persists := 0
    persist := func(Account) error { persists++; return nil }
    _, _, err = a.SignIn(ctx, LoginInput{}, &old, LoginOptions{Store: "keyring", Remember: true}, persist)
    apiCode(t, err, "IDENTITY_MISMATCH")
    if persists != 0 || len(keyring.values) != 1 {
        t.Fatal("changed working login")
    }
    replacement, _, err := a.SignIn(ctx, LoginInput{}, &old, LoginOptions{Store: "keyring", Remember: true, Replace: true}, persist)
    if err != nil || replacement.Credential.Key == old.Credential.Key || len(keyring.values) != 2 {
        t.Fatal(err)
    }
    _, _, err = a.SignIn(ctx, LoginInput{}, nil, LoginOptions{Store: "file", Remember: true}, func(Account) error { return errors.New("publish conflict") })
    if err == nil || len(files.values) != 0 || files.deletes != 1 {
        t.Fatal("orphaned failed login", err)
    }
    a.Authenticate = func(context.Context, LoginInput) (Session, error) { return Session{}, &APIError{Code: "AUTH_FAILED"} }
    _, _, err = a.SignIn(ctx, LoginInput{}, &old, LoginOptions{Store: "file", Remember: true}, persist)
    apiCode(t, err, "AUTH_FAILED")
    if persists != 1 {
        t.Fatal("published failed authentication")
    }
}

func TestLoginFailuresDistinguishVaultAndConfigWrites(t *testing.T) {
    a, keyring, _, _ := testAuthentication()
    keyring.err = errors.New("private native-store diagnostic")
    _, _, err := a.SignIn(context.Background(), LoginInput{}, nil, LoginOptions{Store: "keyring", Remember: true}, func(Account) error { t.Fatal("saved after vault failure"); return nil })
    var api *APIError
    if !errors.As(err, &api) || api.AuthStage != "storage" || strings.Contains(err.Error(), "private") {
        t.Fatal(err)
    }
    keyring.err = nil
    _, _, err = a.SignIn(context.Background(), LoginInput{}, nil, LoginOptions{Store: "keyring", Remember: true}, func(Account) error { return errors.New("private settings error") })
    if !errors.As(err, &api) || api.AuthStage != "settings" || api.Code != "ACCOUNT_SAVE_FAILED" || strings.Contains(err.Error(), "private") {
        t.Fatal(err)
    }
}
