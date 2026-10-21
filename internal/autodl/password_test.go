package autodl

import (
    "context"
    "crypto/sha1"
    "encoding/hex"
    "encoding/json"
    "errors"
    "net/http"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

func passwordFactory(handle func(*http.Request) *http.Response) Factory {
    return Factory{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return handle(r), nil })}}
}
func TestPasswordLoginMainAndSubaccountTicketExchange(t *testing.T) {
    for _, kind := range []string{"main", "sub"} {
        t.Run(kind, func(t *testing.T) {
            username, prefix, userField, wantID := "13800000000", "/api/v1", "username", "user-fixture"
            if kind == "sub" {
                username, prefix, userField, wantID = "student@master", "/api/v1/sub_user", "sub_name", "123"
            }
            calls := []string{}
            password := []byte("password with 空格 !")
            sum := sha1.Sum(password)
            digest := hex.EncodeToString(sum[:])
            f := passwordFactory(func(r *http.Request) *http.Response {
                if r.URL.Host != "www.autodl.com" {
                    t.Fatal(r.URL)
                }
                calls = append(calls, r.Method+" "+r.URL.Path)
                var body object
                if r.Method == "POST" && json.NewDecoder(r.Body).Decode(&body) != nil {
                    t.Fatal("body")
                }
                switch r.URL.Path {
                case "/api/v1/login_failed/count":
                    if text(body, "phone") != username || text(body, "device_fingerprint") != "device-fixture" {
                        t.Fatal("preflight")
                    }
                    return response(200, `{"code":"Success","data":{"sms_2fa":false,"pv":0}}`)
                case prefix + "/login", prefix + "/new_login":
                    field := "phone"
                    if kind == "sub" {
                        field = "sub_name"
                    }
                    if text(body, field) != username || text(body, "password") != digest || r.Header.Get("Authorization") != "" {
                        t.Fatal("login request")
                    }
                    principal := map[string]any{"uuid": "user-fixture", userField: username}
                    if kind == "sub" {
                        principal["uuid"], principal["id"] = "", 123
                    }
                    data, _ := json.Marshal(map[string]any{"code": "Success", "data": map[string]any{"ticket": "ticket-private", "user": principal}})
                    return response(200, string(data))
                case prefix + "/passport":
                    if text(body, "ticket") != "ticket-private" {
                        t.Fatal("ticket")
                    }
                    return response(200, `{"code":"Success","data":{"token":"login-session-secret"}}`)
                case "/api/v1/user/detail", "/api/v1/sub_user":
                    if r.Header.Get("Authorization") != "login-session-secret" {
                        t.Fatal("missing session")
                    }
                    uuid := "user-fixture"
                    if kind == "sub" {
                        uuid = "parent-fixture"
                    }
                    data, _ := json.Marshal(map[string]any{"code": "Success", "data": map[string]any{"id": 123, "uuid": uuid, userField: username, "roles": map[string]string{"instance": "read_write", "instance_pro": "no_permission"}}})
                    return response(200, string(data))
                }
                t.Fatal(r.URL)
                return nil
            })
            session, err := f.Login(context.Background(), cloud.LoginInput{Username: username, Password: password, DeviceID: "device-fixture"})
            want := 4
            if kind == "sub" {
                want = 3
            }
            if err != nil || len(calls) != want || session.Digest != digest || session.Identity.Kind != kind || session.Identity.ID != wantID {
                t.Fatal("login", calls, err)
            }
            if kind == "sub" && (session.Pro == nil || *session.Pro) || kind == "main" && session.Pro != nil {
                t.Fatal("fabricated privilege")
            }
            data, _ := json.Marshal(session)
            if strings.Contains(string(data), "login-session-secret") || strings.Contains(string(data), digest) {
                t.Fatal("public secret")
            }
        })
    }
}
func TestPasswordChallengeAndRejectionAreNeverReplayed(t *testing.T) {
    for _, challenge := range []string{`{"sms_2fa":true,"pv":0}`, `{"sms_2fa":false,"pv":3}`} {
        calls := 0
        f := passwordFactory(func(r *http.Request) *http.Response {
            calls++
            return response(200, `{"code":"Success","data":`+challenge+`}`)
        })
        _, err := f.Login(context.Background(), cloud.LoginInput{Username: "13800000000", Password: []byte("password")})
        var api *cloud.APIError
        if !errors.As(err, &api) || api.Code != "AUTH_CHALLENGE_REQUIRED" || calls != 1 {
            t.Fatal(err, calls)
        }
    }
    calls := 0
    f := passwordFactory(func(r *http.Request) *http.Response {
        calls++
        return response(200, `{"code":"AccountPasswordIsWrong","msg":"password-secret"}`)
    })
    _, err := f.Login(context.Background(), cloud.LoginInput{Username: "student@master", Digest: strings.Repeat("a", 40)})
    var api *cloud.APIError
    if !errors.As(err, &api) || api.Code != "AUTH_FAILED" || calls != 1 || strings.Contains(err.Error(), "password-secret") {
        t.Fatal(err, calls)
    }
}
func TestPasswordLoginRejectsIdentityMismatch(t *testing.T) {
    f := passwordFactory(func(r *http.Request) *http.Response {
        if strings.HasSuffix(r.URL.Path, "/login") {
            return response(200, `{"code":"Success","data":{"ticket":"ticket","user":{"uuid":"","id":123,"sub_name":"student@master"}}}`)
        }
        if strings.HasSuffix(r.URL.Path, "/passport") {
            return response(200, `{"code":"Success","data":{"token":"session"}}`)
        }
        return response(200, `{"code":"Success","data":{"uuid":"same-parent","id":124,"sub_name":"student@master","roles":{"instance":true,"instance_pro":true}}}`)
    })
    _, err := f.Login(context.Background(), cloud.LoginInput{Username: "student@master", Digest: strings.Repeat("a", 40)})
    var api *cloud.APIError
    if !errors.As(err, &api) || api.Code != "IDENTITY_MISMATCH" {
        t.Fatal(err)
    }
}

func TestSubaccountIdentityRequiresItsOwnPositiveIntegerID(t *testing.T) {
    for _, id := range []string{`null`, `0`, `-1`, `1.5`, `"123"`, `18446744073709551616`} {
        var data object
        json.Unmarshal([]byte(`{"uuid":"shared-parent","id":`+id+`,"sub_name":"student@master"}`), &data)
        _, err := principalIdentity(data, "sub")
        var api *cloud.APIError
        if !errors.As(err, &api) || api.Code != "MISSING_PRINCIPAL_ID" {
            t.Fatal("accepted invalid subaccount ID", id, err)
        }
    }
    var data object
    json.Unmarshal([]byte(`{"uuid":"","id":9007199254740993,"sub_name":"student@master"}`), &data)
    identity, err := principalIdentity(data, "sub")
    if err != nil || identity.ID != "9007199254740993" {
        t.Fatal("lost integer precision", identity.ID, err)
    }
}

func TestPasswordLoginRejectsAnotherSubaccountBeforeTicketExchange(t *testing.T) {
    calls := 0
    f := passwordFactory(func(r *http.Request) *http.Response {
        calls++
        return response(200, `{"code":"Success","data":{"ticket":"private-ticket","user":{"uuid":"","id":123,"sub_name":"other@master"}}}`)
    })
    _, err := f.Login(context.Background(), cloud.LoginInput{Username: "student@master", Digest: strings.Repeat("a", 40)})
    var api *cloud.APIError
    if !errors.As(err, &api) || api.Code != "IDENTITY_MISMATCH" || calls != 1 {
        t.Fatal(err, calls)
    }
}

func TestLoginFailuresIdentifyStageWithoutReplayingSecrets(t *testing.T) {
    for _, stage := range []string{"password", "login_response", "passport", "passport_response", "identity"} {
        t.Run(stage, func(t *testing.T) {
            calls := 0
            f := passwordFactory(func(r *http.Request) *http.Response {
                calls++
                if strings.HasSuffix(r.URL.Path, "/login") {
                    if stage == "password" {
                        return response(503, `{"code":"InternalError","msg":"never-print-password"}`)
                    }
                    if stage == "login_response" {
                        return response(200, `{"code":"Success","data":{}}`)
                    }
                    return response(200, `{"code":"Success","data":{"ticket":"private-ticket","user":{"uuid":"","id":123,"sub_name":"student@master"}}}`)
                }
                if strings.HasSuffix(r.URL.Path, "/passport") {
                    if stage == "passport" {
                        return response(200, `{"code":"RequestParameterIsWrong","msg":"private-ticket"}`)
                    }
                    if stage == "passport_response" {
                        return response(200, `{"code":"Success","data":{}}`)
                    }
                    return response(200, `{"code":"Success","data":{"token":"private-token"}}`)
                }
                return response(200, `{"code":"Success","data":{"uuid":"","id":123,"sub_name":"student@master","roles":"unexpected"}}`)
            })
            _, err := f.Login(context.Background(), cloud.LoginInput{Username: "student@master", Digest: strings.Repeat("a", 40)})
            var api *cloud.APIError
            if !errors.As(err, &api) || api.AuthStage != stage || strings.Contains(err.Error(), "private-") {
                t.Fatal(stage, err)
            }
            want := 1
            if strings.HasPrefix(stage, "passport") {
                want = 2
            }
            if stage == "identity" {
                want = 3
            }
            if calls != want {
                t.Fatal("replayed login", calls)
            }
        })
    }
}
