package cloud

import (
    "bytes"
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "io/fs"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/fault"
)

// SecretStore is the side-effect boundary consumed by authentication.
// The selected implementation must not silently change storage or leak secrets.
type SecretStore interface {
    Get(context.Context, string) (string, error)
    Set(context.Context, string, string) error
    Delete(context.Context, string) error
}

// LoginInput contains transient credentials. Digest is AutoDL's replayable SHA-1
// password equivalent, not a salted local password verifier.
type LoginInput struct {
    Username  string `json:"username"`
    PhoneArea string `json:"phone_area"`
    Password  []byte `json:"-"`
    Digest    string `json:"-"`
    Code      string `json:"-"`
    DeviceID  string `json:"-"`
}

// Session is never directly serialized to the vault; public JSON omits secrets.
type Session struct {
    Identity  Identity `json:"identity"`
    Username  string   `json:"username"`
    PhoneArea string   `json:"phone_area"`
    DeviceID  string   `json:"-"`
    Token     string   `json:"-"`
    Digest    string   `json:"-"`
    Standard  bool     `json:"standard"`
    Pro       *bool    `json:"pro,omitempty"`
}

// Authentication composes one authenticator and two explicit secret stores. Account
// persistence remains the caller's concrete config store, not a Repository.
type Authentication struct {
    Authenticate func(context.Context, LoginInput) (Session, error)
    Keyring      SecretStore
    Files        SecretStore
}

// LoginOptions selects persistence without changing the authentication protocol.
type LoginOptions struct {
    Store    string
    Replace  bool
    Remember bool
}

func (a *Authentication) store(name string) (SecretStore, error) {
    var s SecretStore
    switch name {
    case "keyring":
        s = a.Keyring
    case "file":
        s = a.Files
    }
    if s == nil {
        return nil, &APIError{Code: "CREDENTIAL_STORE_UNAVAILABLE"}
    }
    return s, nil
}

func credentialError(err error) error {
    if err == nil {
        return nil
    }
    if errors.Is(err, fs.ErrNotExist) {
        return &APIError{Code: "AUTH_REQUIRED"}
    }
    if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
        return err
    }
    return &APIError{Code: "CREDENTIAL_STORE_UNAVAILABLE"}
}

// loginFailure adds only fixed diagnostic labels, never a storage/provider
// diagnostic string. Existing configuration error codes keep their exit policy.
func loginFailure(err error, stage, code string) error {
    var api *APIError
    if errors.As(err, &api) {
        copy := *api
        if copy.AuthStage == "" {
            copy.AuthStage = stage
        }
        return &copy
    }
    var f *fault.Error
    if errors.As(err, &f) {
        copy := *f
        copy.Params = map[string]any{}
        for key, value := range f.Params {
            copy.Params[key] = value
        }
        copy.Params["AuthStage"], copy.Params["ProviderCode"], copy.Params["HTTPStatus"] = stage, code, 0
        copy.Hint = "cloud_login_diagnostic"
        return &copy
    }
    if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
        return err
    }
    return &APIError{Code: code, AuthStage: stage}
}

type sessionRecord struct {
    Version   int      `json:"format_version"`
    Identity  Identity `json:"identity"`
    Username  string   `json:"username"`
    PhoneArea string   `json:"phone_area"`
    DeviceID  string   `json:"device_id"`
    Token     string   `json:"token"`
    Digest    string   `json:"password_sha1,omitempty"`
    Standard  bool     `json:"standard"`
    Pro       *bool    `json:"pro,omitempty"`
}

// NewCredentialKey returns an opaque private-vault key, not an account alias.
func NewCredentialKey() (string, error) {
    var b [12]byte
    if _, err := rand.Read(b[:]); err != nil {
        return "", err
    }
    return "s_" + hex.EncodeToString(b[:]), nil
}

// Read refuses unknown records and identity changes without mutating the vault.
func (a *Authentication) Read(ctx context.Context, ref CredentialRef) (Session, error) {
    var s Session
    if ref.Validate() != nil || ref.Key == "" {
        return s, &APIError{Code: "CREDENTIAL_STORE_UNAVAILABLE"}
    }
    store, err := a.store(ref.Store)
    if err != nil {
        return s, err
    }
    data, err := store.Get(ctx, ref.Key)
    if err != nil {
        return s, credentialError(err)
    }
    var r sessionRecord
    d := json.NewDecoder(bytes.NewBufferString(data))
    d.DisallowUnknownFields()
    if len(data) > 16*1024 || d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.Version != 1 || r.Identity != ref.Identity || r.Token == "" || strings.ContainsAny(r.Token, "\r\n\x00") || r.Username == "" || strings.ContainsAny(r.Username, "\r\n\x00") || (r.Digest != "" && !validDigest(r.Digest)) {
        return s, &APIError{Code: "INVALID_CREDENTIAL_RECORD"}
    }
    return Session{Identity: r.Identity, Username: r.Username, PhoneArea: r.PhoneArea, DeviceID: r.DeviceID, Token: r.Token, Digest: r.Digest, Standard: r.Standard, Pro: r.Pro}, nil
}

func validDigest(s string) bool {
    b, err := hex.DecodeString(s)
    return err == nil && len(b) == 20 && s == strings.ToLower(s)
}

// Token is used by provider composition; it never renders a vault record.
func (a *Authentication) Token(ctx context.Context, ref CredentialRef) (string, error) {
    s, err := a.Read(ctx, ref)
    return s.Token, err
}

// Save stores only a verified session and optionally the protocol digest. A new
// principal always gets a new key; callers choose whether to replace an alias.
func (a *Authentication) Save(ctx context.Context, s Session, previous *Account, source string, remember bool) (Account, error) {
    out := Account{Provider: "autodl", DefaultProduct: "standard"}
    if previous != nil {
        out = *previous
    }
    store, err := a.store(source)
    if err != nil {
        return Account{}, err
    }
    key := ""
    if previous != nil && previous.Credential.Mode == "session" && previous.Credential.Identity == s.Identity && previous.Credential.Store == source {
        key = previous.Credential.Key
        if key != "" {
            if _, err := a.Read(ctx, previous.Credential); err != nil {
                var api *APIError
                if !errors.As(err, &api) || api.Code != "AUTH_REQUIRED" {
                    return Account{}, err
                }
            }
        }
    }
    if key == "" {
        var err error
        key, err = NewCredentialKey()
        if err != nil {
            return Account{}, err
        }
    }
    out.Credential = CredentialRef{Mode: "session", Key: key, Store: source, Identity: s.Identity}
    if out.Validate() != nil || s.Username == "" || len(s.Username) > 512 || strings.ContainsAny(s.Username+s.PhoneArea+s.DeviceID, "\r\n\x00") || s.Token == "" || strings.ContainsAny(s.Token, "\r\n\x00") {
        return Account{}, &APIError{Code: "INVALID_CREDENTIAL_RECORD"}
    }
    digest := ""
    if remember {
        digest = s.Digest
        if !validDigest(digest) {
            return Account{}, &APIError{Code: "INVALID_CREDENTIAL_RECORD"}
        }
    }
    r := sessionRecord{Version: 1, Identity: s.Identity, Username: s.Username, PhoneArea: s.PhoneArea, DeviceID: s.DeviceID, Token: s.Token, Digest: digest, Standard: s.Standard, Pro: s.Pro}
    data, err := json.Marshal(r)
    if err != nil {
        return Account{}, err
    }
    if err := store.Set(ctx, key, string(data)); err != nil {
        return Account{}, credentialError(err)
    }
    return out, nil
}

// SignIn leaves existing credentials untouched on failed authentication or an
// unauthorized principal change. The caller atomically publishes the reference.
func (a *Authentication) SignIn(ctx context.Context, in LoginInput, previous *Account, options LoginOptions, persist func(Account) error) (Account, Session, error) {
    if a.Authenticate == nil || !ValidCredentialStore(options.Store) {
        return Account{}, Session{}, &APIError{Code: "CREDENTIAL_STORE_UNAVAILABLE"}
    }
    if _, err := a.store(options.Store); err != nil {
        return Account{}, Session{}, err
    }
    session, err := a.Authenticate(ctx, in)
    if err != nil {
        return Account{}, Session{}, loginFailure(err, "password", "AUTHENTICATION_FAILED")
    }
    if previous != nil && previous.Credential.Identity != session.Identity && !options.Replace {
        return Account{}, Session{}, &APIError{Code: "IDENTITY_MISMATCH"}
    }
    account, err := a.Save(ctx, session, previous, options.Store, options.Remember)
    if err != nil {
        return Account{}, Session{}, loginFailure(err, "storage", "CREDENTIAL_STORE_UNAVAILABLE")
    }
    if err := persist(account); err != nil {
        if previous == nil || previous.Credential.Key != account.Credential.Key || previous.Credential.Store != account.Credential.Store {
            cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
            _ = a.Forget(cleanup, account.Credential)
            cancel()
        }
        return Account{}, Session{}, loginFailure(err, "settings", "ACCOUNT_SAVE_FAILED")
    }
    return account, session, nil
}

// Forget clears only this explicitly selected stored session, not other accounts
// or platform sessions. Bound runs retain references and now need re-login.
func (a *Authentication) Forget(ctx context.Context, ref CredentialRef) error {
    if ref.Key == "" || ref.Validate() != nil {
        return fault.New("BAD_CONFIG", "cloud_saved_login_required", nil)
    }
    store, err := a.store(ref.Store)
    if err != nil {
        return err
    }
    err = store.Delete(ctx, ref.Key)
    if errors.Is(err, fs.ErrNotExist) {
        return nil
    }
    return credentialError(err)
}
