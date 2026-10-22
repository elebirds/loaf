package cloud

import (
    "regexp"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/fault"
)

// Settings is user-owned; projects cannot raise Safety's spending ceiling.
type Settings struct {
    DefaultAccount  string             `toml:"default_account,omitempty"`
    CredentialStore string             `toml:"credential_store,omitempty"`
    Defaults        Profile            `toml:"defaults,omitempty"`
    Safety          Ceiling            `toml:"safety,omitempty"`
    Accounts        map[string]Account `toml:"accounts,omitempty"`
}

// Ceiling is independent of convenience defaults and applies last.
type Ceiling struct {
    MaxComputeHourlyPrice string `toml:"max_compute_hourly_price,omitempty"`
    Currency              string `toml:"currency,omitempty"`
}

// Profile stores optional per-field overrides; nil and explicit zero differ.
type Profile struct {
    Product               *string       `toml:"product,omitempty"`
    GPUVendor             *string       `toml:"gpu_vendor,omitempty"`
    GPUModels             []string      `toml:"gpu_models,omitempty"`
    GPUCount              *int          `toml:"gpu_count,omitempty"`
    MinGPUMemoryGiB       *int          `toml:"min_gpu_memory_gib,omitempty"`
    MinCPUs               *int          `toml:"min_cpus,omitempty"`
    MinMemoryGiB          *int          `toml:"min_memory_gib,omitempty"`
    MinDiskGiB            *int          `toml:"min_disk_gib,omitempty"`
    Regions               []string      `toml:"regions,omitempty"`
    MaxComputeHourlyPrice *string       `toml:"max_compute_hourly_price,omitempty"`
    Currency              *string       `toml:"currency,omitempty"`
    WaitTimeout           *string       `toml:"wait_timeout,omitempty"`
    PollInterval          *string       `toml:"poll_interval,omitempty"`
    AutoDL                AutoDLProfile `toml:"autodl,omitempty"`
}

// AutoDLProfile keeps actual platform image inputs explicit, without a DSL.
type AutoDLProfile struct {
    Image              *string `toml:"image,omitempty"`
    PrivateImageUUID   *string `toml:"private_image_uuid,omitempty"`
    GPUSpecID          *int    `toml:"gpu_spec_id,omitempty"`
    GPUSpecUUID        *string `toml:"gpu_spec_uuid,omitempty"`
    CUDAFrom           *int    `toml:"cuda_from,omitempty"`
    ExpandSystemDiskGB *int    `toml:"expand_system_disk_gb,omitempty"`
    ExpandDataDiskGB   *int    `toml:"expand_data_disk_gb,omitempty"`
}

// Requirements are hard filters, not preferences that can be silently relaxed.
type Requirements struct {
    GPUVendor             string   `json:"gpu_vendor,omitempty"`
    GPUModels             []string `json:"gpu_models,omitempty"`
    GPUCount              int      `json:"gpu_count"`
    MinGPUMemoryGiB       int      `json:"min_gpu_memory_gib"`
    MinCPUs               int      `json:"min_cpus"`
    MinMemoryGiB          int      `json:"min_memory_gib"`
    MinDiskGiB            int      `json:"min_disk_gib"`
    Regions               []string `json:"regions,omitempty"`
    MaxComputeHourlyPrice *Money   `json:"max_compute_hourly_price,omitempty"`
}

// ImageOptions are data, not a platform startup command for running training.
type ImageOptions struct {
    Image              string `json:"image,omitempty"`
    PrivateImageUUID   string `json:"private_image_uuid,omitempty"`
    GPUSpecID          int    `json:"gpu_spec_id,omitempty"`
    GPUSpecUUID        string `json:"gpu_spec_uuid,omitempty"`
    CUDAFrom           int    `json:"cuda_from,omitempty"`
    ExpandSystemDiskGB int    `json:"expand_system_disk_gb"`
    ExpandDataDiskGB   int    `json:"expand_data_disk_gb"`
}

// Plan is frozen before monitoring or authorizing a purchase.
type Plan struct {
    Product      string       `json:"product"`
    Requirements Requirements `json:"requirements"`
    AutoDL       ImageOptions `json:"autodl"`
    WaitTimeout  string       `json:"wait_timeout"`
    PollInterval string       `json:"poll_interval"`
    Clone        *CloneSource `json:"clone,omitempty"`
}

func invalid() error { return fault.New("BAD_CONFIG", "cloud_config", nil) }

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var platformID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ValidProduct is deliberately limited to the two implemented products.
func ValidProduct(product string) bool { return product == "standard" || product == "pro" }

// ValidCredentialStore limits selection to the two shipped storage methods.
func ValidCredentialStore(store string) bool { return store == "keyring" || store == "file" }

// Validate keeps authentication mode, principal and secret location separate.
func (r CredentialRef) Validate() error {
    if r.Mode != "session" && r.Mode != "developer" || (r.Env == "") == (r.Key == "") {
        return invalid()
    }
    if r.Env != "" && !envName.MatchString(r.Env) || r.Key != "" && !platformID.MatchString(r.Key) {
        return invalid()
    }
    if r.Env != "" && r.Store != "" || r.Key != "" && !ValidCredentialStore(r.Store) {
        return invalid()
    }
    identity := r.Identity
    if identity != (Identity{}) {
        if identity.Kind != "main" && identity.Kind != "sub" || !platformID.MatchString(identity.ID) || identity.Username == "" || len(identity.Username) > 512 || strings.ContainsAny(identity.Username, "\x00\r\n") {
            return invalid()
        }
    }
    if r.Mode == "developer" && identity != (Identity{}) || r.Key != "" && (r.Mode != "session" || identity == (Identity{})) {
        return invalid()
    }
    return nil
}

// Validate rejects unrecognized credential declarations.
func (a Account) Validate() error {
    if a.Provider != "autodl" || !ValidProduct(a.DefaultProduct) || a.Credential.Validate() != nil || strings.ContainsAny(a.AppVersion, "\r\n\x00") {
        return invalid()
    }
    return nil
}

// Resolve overlays each scalar independently and replaces entire allowlists.
func Resolve(safety Ceiling, layers ...Profile) (Plan, error) {
    r := Requirements{GPUCount: 1}
    p := Plan{WaitTimeout: "0s", PollInterval: "10s"}
    price, currency := "", "CNY"
    for _, v := range layers {
        if v.Product != nil {
            if !ValidProduct(*v.Product) {
                return p, invalid()
            }
            p.Product = *v.Product
        }
        if v.GPUVendor != nil {
            r.GPUVendor = strings.ToLower(*v.GPUVendor)
        }
        if v.GPUModels != nil {
            r.GPUModels = append([]string{}, v.GPUModels...)
        }
        if v.Regions != nil {
            r.Regions = append([]string{}, v.Regions...)
        }
        if v.GPUCount != nil {
            r.GPUCount = *v.GPUCount
        }
        if v.MinGPUMemoryGiB != nil {
            r.MinGPUMemoryGiB = *v.MinGPUMemoryGiB
        }
        if v.MinCPUs != nil {
            r.MinCPUs = *v.MinCPUs
        }
        if v.MinMemoryGiB != nil {
            r.MinMemoryGiB = *v.MinMemoryGiB
        }
        if v.MinDiskGiB != nil {
            r.MinDiskGiB = *v.MinDiskGiB
        }
        if v.MaxComputeHourlyPrice != nil {
            price = *v.MaxComputeHourlyPrice
        }
        if v.Currency != nil {
            currency = *v.Currency
        }
        if v.WaitTimeout != nil {
            p.WaitTimeout = *v.WaitTimeout
        }
        if v.PollInterval != nil {
            p.PollInterval = *v.PollInterval
        }
        if v.AutoDL.Image != nil {
            p.AutoDL.Image = *v.AutoDL.Image
        }
        if v.AutoDL.PrivateImageUUID != nil {
            p.AutoDL.PrivateImageUUID = *v.AutoDL.PrivateImageUUID
        }
        if v.AutoDL.GPUSpecID != nil {
            p.AutoDL.GPUSpecID = *v.AutoDL.GPUSpecID
        }
        if v.AutoDL.GPUSpecUUID != nil {
            p.AutoDL.GPUSpecUUID = *v.AutoDL.GPUSpecUUID
        }
        if v.AutoDL.CUDAFrom != nil {
            p.AutoDL.CUDAFrom = *v.AutoDL.CUDAFrom
        }
        if v.AutoDL.ExpandSystemDiskGB != nil {
            p.AutoDL.ExpandSystemDiskGB = *v.AutoDL.ExpandSystemDiskGB
        }
        if v.AutoDL.ExpandDataDiskGB != nil {
            p.AutoDL.ExpandDataDiskGB = *v.AutoDL.ExpandDataDiskGB
        }
    }
    if currency != "CNY" || r.GPUCount < 1 || r.GPUCount > 64 || r.MinGPUMemoryGiB < 0 || r.MinCPUs < 0 || r.MinMemoryGiB < 0 || r.MinDiskGiB < 0 {
        return p, invalid()
    }
    if p.AutoDL.ExpandSystemDiskGB < 0 || p.AutoDL.ExpandSystemDiskGB > 500 || p.AutoDL.ExpandDataDiskGB < 0 || p.AutoDL.ExpandDataDiskGB > int(^uint64(0)>>1)>>30 || p.AutoDL.CUDAFrom < 0 || p.AutoDL.GPUSpecID < 0 {
        return p, invalid()
    }
    for _, list := range [][]string{r.GPUModels, r.Regions} {
        if len(list) > 64 {
            return p, invalid()
        }
        for _, s := range list {
            if s == "" || strings.ContainsAny(s, "\x00\r\n") {
                return p, invalid()
            }
        }
    }
    if price != "" {
        m, err := ParseMoney(price, currency)
        if err != nil {
            return p, err
        }
        r.MaxComputeHourlyPrice = &m
    }
    if safety.MaxComputeHourlyPrice != "" {
        if safety.Currency != "CNY" {
            return p, invalid()
        }
        m, err := ParseMoney(safety.MaxComputeHourlyPrice, safety.Currency)
        if err != nil {
            return p, err
        }
        if r.MaxComputeHourlyPrice == nil || Less(m, *r.MaxComputeHourlyPrice) {
            r.MaxComputeHourlyPrice = &m
        }
    } else if safety.Currency != "" {
        return p, invalid()
    }
    wait, err := time.ParseDuration(p.WaitTimeout)
    if err != nil || wait < 0 || wait > 24*time.Hour {
        return p, invalid()
    }
    poll, err := time.ParseDuration(p.PollInterval)
    if err != nil || poll < 10*time.Second || poll > 10*time.Minute {
        return p, invalid()
    }
    p.Requirements = r
    return p, nil
}
