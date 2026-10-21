package autodl

import (
    "context"
    "encoding/json"
    "strconv"

    "github.com/elebirds/loaf/internal/cloud"
)

func allowedRole(raw json.RawMessage) bool {
    var value string
    if json.Unmarshal(raw, &value) == nil {
        return value != "" && value != "no_permission"
    }
    var yes bool
    return json.Unmarshal(raw, &yes) == nil && yes
}

// The subaccount login's uuid is empty, and its profile's uuid identifies the
// parent. Only the numeric id identifies the subaccount itself in both replies.
func principalIdentity(data object, kind string) (cloud.Identity, error) {
    identity := cloud.Identity{Kind: kind, ID: text(data, "uuid"), Username: text(data, "username")}
    if kind == "sub" {
        identity.ID = ""
        identity.Username = text(data, "sub_name")
        var id uint64
        if json.Unmarshal(data["id"], &id) == nil && id > 0 {
            identity.ID = strconv.FormatUint(id, 10)
        }
    }
    if identity.ID == "" {
        return identity, &cloud.APIError{Code: "MISSING_PRINCIPAL_ID"}
    }
    if identity.Username == "" {
        return identity, &cloud.APIError{Code: "MISSING_PRINCIPAL_NAME"}
    }
    if (cloud.CredentialRef{Mode: "session", Key: "identity-verification", Store: "keyring", Identity: identity}).Validate() != nil {
        return identity, &cloud.APIError{Code: "INVALID_LOGIN_PRINCIPAL"}
    }
    return identity, nil
}

func (a *api) inspectIdentity(ctx context.Context) (cloud.Identity, bool, *bool, error) {
    path := "/api/v1/user/detail"
    kind := "main"
    if a.settings.Credential.Identity.Kind == "sub" {
        path = "/api/v1"
        kind = "sub"
    }
    raw, err := a.request(ctx, "GET", path, map[string]string{}, false)
    if err != nil {
        return cloud.Identity{}, false, nil, err
    }
    var data object
    if json.Unmarshal(raw, &data) != nil {
        return cloud.Identity{}, false, nil, &cloud.APIError{Code: "INVALID_RESPONSE"}
    }
    identity, err := principalIdentity(data, kind)
    if err != nil {
        return identity, false, nil, err
    }
    standard := true
    var pro *bool
    if kind == "sub" {
        var roles object
        if json.Unmarshal(data["roles"], &roles) != nil {
            return cloud.Identity{}, false, nil, &cloud.APIError{Code: "INVALID_PERMISSIONS"}
        }
        standard = allowedRole(roles["instance"])
        yes := allowedRole(roles["instance_pro"])
        pro = &yes
    }
    return identity, standard, pro, nil
}
