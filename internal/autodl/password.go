package autodl

import (
    "context"
    "crypto/sha1" // AutoDL's password protocol requires SHA-1; not a local verifier.
    "encoding/hex"
    "encoding/json"
    "errors"
    "runtime"
    "strings"

    "github.com/elebirds/loaf/internal/cloud"
)

func loginError(err error) error {
    var api *cloud.APIError
    if errors.As(err, &api) && (api.Code == "PROVIDER_REJECTED" || api.Code == "AUTH_REQUIRED") {
        return &cloud.APIError{Code: "AUTH_FAILED", HTTPStatus: api.HTTPStatus}
    }
    return err
}

// Login authenticates once and exchanges its ticket once, then reads the actual
// principal. It never guesses a parent login or creates a developer token.
func (f Factory) Login(ctx context.Context, in cloud.LoginInput) (out cloud.Session, err error) {
    stage := "input"
    defer func() {
        var api *cloud.APIError
        if errors.As(err, &api) {
            // Copy rather than mutating an error that may be shared by a caller.
            annotated := *api
            annotated.AuthStage = stage
            err = &annotated
        }
    }()
    if in.Username == "" || len(in.Username) > 512 || strings.ContainsAny(in.Username, "\x00\r\n") {
        return out, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    kind := "main"
    if strings.Contains(in.Username, "@") {
        kind = "sub"
    }
    digest := in.Digest
    if len(in.Password) > 0 {
        sum := sha1.Sum(in.Password)
        digest = hex.EncodeToString(sum[:])
    }
    rawDigest, err := hex.DecodeString(digest)
    if err != nil || len(rawDigest) != 20 || digest != strings.ToLower(digest) {
        return out, &cloud.APIError{Code: "AUTH_REQUIRED"}
    }
    if in.PhoneArea == "" {
        in.PhoneArea = "+86"
    }
    if in.DeviceID == "" {
        in.DeviceID, err = cloud.NewCredentialKey()
        if err != nil {
            return out, err
        }
    }
    settings := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Identity: cloud.Identity{Kind: kind}}}
    a := f.newAPI(settings, "standard", "")
    path := "/api/v1/new_login"
    body := map[string]any{"phone": in.Username, "phone_area": in.PhoneArea, "password": digest, "v_code": in.Code, "device_fingerprint": in.DeviceID, "captcha_verify_param": nil}
    if kind == "sub" {
        path = "/api/v1/login" // The sub namespace produces /sub_user/login.
        body = map[string]any{"sub_name": in.Username, "password": digest}
    } else if in.Code == "" {
        stage = "challenge"
        raw, e := a.request(ctx, "POST", "/api/v1/login_failed/count", map[string]string{"phone": in.Username, "device_fingerprint": in.DeviceID}, false)
        if e != nil {
            return out, loginError(e)
        }
        var status struct {
            SMS bool `json:"sms_2fa"`
            PV  int  `json:"pv"`
        }
        if json.Unmarshal(raw, &status) != nil {
            return out, &cloud.APIError{Code: "INVALID_RESPONSE"}
        }
        if status.SMS || status.PV >= 3 {
            return out, &cloud.APIError{Code: "AUTH_CHALLENGE_REQUIRED"}
        }
    }
    stage = "password"
    raw, err := a.request(ctx, "POST", path, body, false)
    if err != nil {
        return out, loginError(err)
    }
    stage = "login_response"
    var accepted struct {
        Ticket string `json:"ticket"`
        User   object `json:"user"`
    }
    if json.Unmarshal(raw, &accepted) != nil || accepted.Ticket == "" || strings.ContainsAny(accepted.Ticket, "\x00\r\n") {
        return out, &cloud.APIError{Code: "INVALID_LOGIN_TICKET"}
    }
    identity, err := principalIdentity(accepted.User, kind)
    if err != nil {
        return out, err
    }
    if kind == "sub" && identity.Username != in.Username {
        return out, &cloud.APIError{Code: "IDENTITY_MISMATCH"}
    }
    stage = "passport"
    raw, err = a.request(ctx, "POST", "/api/v1/passport", map[string]any{"ticket": accepted.Ticket, "device": map[string]any{"os": map[string]string{"name": runtime.GOOS}, "browser": map[string]string{"name": "Loaf", "version": "1", "major": "1"}}}, false)
    if err != nil {
        return out, loginError(err)
    }
    stage = "passport_response"
    var exchanged struct {
        Token string `json:"token"`
    }
    if json.Unmarshal(raw, &exchanged) != nil || exchanged.Token == "" || strings.ContainsAny(exchanged.Token, "\x00\r\n") {
        return out, &cloud.APIError{Code: "INVALID_SESSION_TOKEN"}
    }
    a.token = exchanged.Token
    stage = "identity"
    actual, standard, pro, err := a.inspectIdentity(ctx)
    if err != nil {
        return out, err
    }
    if actual != identity {
        return out, &cloud.APIError{Code: "IDENTITY_MISMATCH"}
    }
    return cloud.Session{Identity: actual, Username: in.Username, PhoneArea: in.PhoneArea, DeviceID: in.DeviceID, Token: exchanged.Token, Digest: digest, Standard: standard, Pro: pro}, nil
}
