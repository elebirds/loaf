// Package cloud coordinates explicit compute acquisition, independently of SSH
// experiment execution and of any particular platform's HTTP API.
package cloud

import (
    "context"
    "errors"
    "time"

    "github.com/elebirds/loaf/internal/fault"
)

// Identity is the authenticated platform principal, independently of the compute
// product. An empty identity is allowed for explicitly supplied environment
// credentials whose principal has not yet been verified by a login operation.
type Identity struct {
    Kind     string `toml:"kind,omitempty" json:"kind,omitempty"`
    ID       string `toml:"id,omitempty" json:"id,omitempty"`
    Username string `toml:"username,omitempty" json:"username,omitempty"`
}

// CredentialRef names a session or developer credential, never its secret value.
// Exactly one of Env and Key is set. Store selects the key's storage location;
// those credentials must carry the verified identity obtained during login.
type CredentialRef struct {
    Mode     string   `toml:"mode" json:"mode"`
    Env      string   `toml:"env,omitempty" json:"env,omitempty"`
    Key      string   `toml:"key,omitempty" json:"key,omitempty"`
    Store    string   `toml:"store,omitempty" json:"store,omitempty"`
    Identity Identity `toml:"identity,omitempty" json:"identity,omitempty"`
}

// Account is a user-owned connection preference. DefaultProduct is only a
// default: each resource reference freezes its own selected product.
type Account struct {
    Provider       string        `toml:"provider" json:"provider"`
    DefaultProduct string        `toml:"default_product" json:"default_product"`
    Credential     CredentialRef `toml:"credential" json:"credential"`
    AppVersion     string        `toml:"app_version,omitempty" json:"app_version,omitempty"`
}

// OpenProvider composes a product adapter using the saved account's credential.
// Context also bounds credential-store access; it does not permit API fallback.
type OpenProvider func(context.Context, string, Account, string) (Provider, error)

// Ref identifies one immutable resource, not a movable target alias.
type Ref struct {
    Provider string `json:"provider" toml:"provider"`
    Product  string `json:"product" toml:"product"`
    Account  string `json:"account" toml:"account"`
    ID       string `json:"id" toml:"id"`
}

// Binding freezes the instance and credential reference in target/run locators.
// Updating or removing an alias cannot redirect previously submitted runs.
type Binding struct {
    Ref      Ref     `json:"instance" toml:"instance"`
    Settings Account `json:"account_settings" toml:"account_settings"`
}

// Instance is a public, deliberately allowlisted observation.
type Instance struct {
    Aliases         []string  `json:"aliases,omitempty"`
    Ref             Ref       `json:"instance"`
    Name            string    `json:"name,omitempty"`
    Status          string    `json:"status"`
    NativeStatus    string    `json:"native_status"`
    Region          string    `json:"region,omitempty"`
    CloneDataStatus string    `json:"clone_data_status,omitempty"`
    ObservedAt      time.Time `json:"observed_at"`
}

// Connection holds transient authentication material. JSON serialization always
// redacts the password; only the explicit credentials command may reveal it.
type Connection struct {
    Host     string `json:"host"`
    Port     int    `json:"port"`
    User     string `json:"user"`
    Password string `json:"-"`
}

// Page reports coverage rather than presenting a truncated list as complete.
type Page struct {
    Account   string     `json:"account,omitempty"`
    Product   string     `json:"product,omitempty"`
    Instances []Instance `json:"instances"`
    Examined  int        `json:"examined"`
    Complete  bool       `json:"complete"`
}

// Provider is the lifecycle boundary needed by the resource use cases.
// Inventory is separate: a platform without a trustworthy catalog is not stock.
type Provider interface {
    List(context.Context, int) (Page, error)
    Get(context.Context, Ref) (Instance, error)
    Connection(context.Context, Ref) (Connection, error)
    Create(context.Context, Plan, *Offer) (Ref, error)
    Start(context.Context, Ref) error
    Stop(context.Context, Ref) error
}

// Inventory is an optional read-only offer capability.
type Inventory interface {
    Search(context.Context, Plan, int) (InventoryPage, error)
}

// Cloner is optional: copying an existing instance is not ordinary creation.
type Cloner interface {
    CloneSource(context.Context, Ref) (CloneSource, error)
    Clone(context.Context, Plan, *Offer) (Ref, error)
}

// CloneSource freezes public source identity and image constraints, not private
// platform records, credentials or a snapshot of the source filesystem.
type CloneSource struct {
    Instance         Ref    `json:"instance"`
    Region           string `json:"region"`
    Image            string `json:"image"`
    Vendor           string `json:"gpu_vendor"`
    Architecture     string `json:"architecture"`
    CUDA             string `json:"cuda,omitempty"`
    ExpandDataDiskGB int    `json:"expand_data_disk_gb"`
}

// Offer retains unknown quantities as nil. GPU memory is per card, while Price
// is the whole requested instance's compute fee per hour, excluding storage.
type Offer struct {
    Architecture       string    `json:"architecture,omitempty"`
    Driver             string    `json:"driver,omitempty"`
    HighestCUDA        string    `json:"highest_cuda,omitempty"`
    ID                 string    `json:"id"`
    Vendor             string    `json:"gpu_vendor,omitempty"`
    Model              string    `json:"gpu_model,omitempty"`
    GPUCount           int       `json:"gpu_count"`
    GPUMemoryGiB       *int      `json:"gpu_memory_gib,omitempty"`
    CPUs               *int      `json:"cpus,omitempty"`
    MemoryGiB          *int      `json:"memory_gib,omitempty"`
    DiskGiB            *int      `json:"disk_gib,omitempty"`
    Region             string    `json:"region,omitempty"`
    Available          *bool     `json:"available,omitempty"`
    ImageAvailable     *bool     `json:"image_available,omitempty"`
    ComputeHourlyPrice *Money    `json:"compute_hourly_price,omitempty"`
    StorageDailyPrice  *Money    `json:"storage_daily_price,omitempty"`
    Source             string    `json:"source"`
    ObservedAt         time.Time `json:"observed_at"`
}

// InventoryPage is one bounded catalog observation, not a reservation.
type InventoryPage struct {
    ScanOffset int       `json:"scan_offset,omitempty"`
    NextOffset int       `json:"next_offset,omitempty"`
    HasMore    bool      `json:"has_more,omitempty"`
    Offers     []Offer   `json:"offers"`
    Examined   int       `json:"examined"`
    Complete   bool      `json:"complete"`
    ObservedAt time.Time `json:"observed_at"`
}

// APIError deliberately carries no response body, request token or password.
// Only a confirmed mapped NoStock permits retrying an allocation. A proven
// pre-mutation read failure can instead resume monitoring without replaying one.
type APIError struct {
    BeforeMutation bool
    Code           string
    AuthStage      string
    HTTPStatus     int
    RetryAfter     time.Duration
    Transient      bool
    Unconfirmed    bool
    NoStock        bool
}

func (e *APIError) Error() string { return "cloud API: " + e.Code }

// Failure converts internal API outcomes into the stable CLI contract.
func Failure(err error) error {
    if err == nil {
        return nil
    }
    var f *fault.Error
    existing := errors.As(err, &f)
    code := "CLOUD_FAILED"
    message := "cloud_failed"
    var api *APIError
    hasAPI := errors.As(err, &api)
    if hasAPI {
        if api.Unconfirmed {
            code, message = "CLOUD_UNCONFIRMED", "cloud_unconfirmed"
        }
        if api.Code == "AUTH_REQUIRED" {
            code, message = "CLOUD_AUTH_REQUIRED", "cloud_auth_required"
        }
        if api.Code == "UNSUPPORTED_CAPABILITY" {
            code, message = "UNSUPPORTED_CAPABILITY", "cloud_unsupported"
        }
        if api.Code == "UNSUPPORTED_AUTH_PRODUCT" {
            code, message = "UNSUPPORTED_CAPABILITY", "cloud_auth_product_unsupported"
        }
        if api.Code == "CREDENTIAL_STORE_UNAVAILABLE" {
            code, message = "CREDENTIAL_STORE_UNAVAILABLE", "cloud_credential_store_unavailable"
        }
        switch api.Code {
        case "PRICE_UNVERIFIABLE":
            code, message = "PRICE_UNVERIFIABLE", "cloud_price_unverifiable"
        case "OFFER_CHANGED":
            code, message = "NO_MATCH", "cloud_no_match"
        case "AUTH_CHALLENGE_REQUIRED":
            code, message = "AUTH_CHALLENGE_REQUIRED", "cloud_auth_challenge"
        case "AUTH_FAILED":
            code, message = "CLOUD_AUTH_FAILED", "cloud_auth_failed"
        case "IDENTITY_MISMATCH":
            code, message = "IDENTITY_MISMATCH", "cloud_identity_mismatch"
        case "INVALID_CREDENTIAL_RECORD":
            code, message = "INVALID_CREDENTIAL_RECORD", "cloud_credential_invalid"
        case "CLONE_SOURCE_NOT_STOPPED":
            code, message = api.Code, "cloud_clone_source_stopped"
        case "CLONE_SOURCE_CHANGED":
            code, message = api.Code, "cloud_clone_source_changed"
        case "CLONE_SOURCE_INVALID":
            code, message = api.Code, "cloud_clone_source_invalid"
        }
    }
    if hasAPI && api.AuthStage != "" && code == "CLOUD_FAILED" {
        code, message = "CLOUD_LOGIN_FAILED", "cloud_login_failed"
    }
    if !existing {
        f = fault.Wrap(code, message, err)
    }
    if hasAPI {
        if f.Params == nil {
            f.Params = map[string]any{}
        }
        f.Params["ProviderCode"] = api.Code
        f.Params["HTTPStatus"] = api.HTTPStatus
        if api.AuthStage != "" {
            f.Params["AuthStage"] = api.AuthStage
            f.Hint = "cloud_login_diagnostic"
        }
    }
    if f.Code == "CLOUD_UNCONFIRMED" {
        f.Hint = "cloud_reconcile_hint"
    }
    return f
}
