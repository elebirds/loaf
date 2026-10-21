// Package autodl adapts ordinary/Pro web sessions and the Pro developer API.
// It never sends platform credentials to the remote experiment runner.
package autodl

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "net/url"
    "regexp"
    "strconv"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
)

// Factory manually composes the two concrete products. HTTP is an injectable
// transport for contract tests, not a configurable platform/token destination.
type Factory struct {
    HTTP       *http.Client
    LookupEnv  func(string) (string, bool)
    ReadSecret func(context.Context, cloud.CredentialRef) (string, error)
}

// Open resolves a user-owned credential reference only when an API is needed.
func (f Factory) Open(ctx context.Context, name string, a cloud.Account, product string) (cloud.Provider, error) {
    if err := a.Validate(); err != nil {
        return nil, err
    }
    if !cloud.ValidProduct(product) {
        return nil, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    if a.Credential.Mode == "developer" && product != "pro" {
        return nil, &cloud.APIError{Code: "UNSUPPORTED_AUTH_PRODUCT"}
    }
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    token := ""
    if a.Credential.Env != "" && f.LookupEnv != nil {
        token, _ = f.LookupEnv(a.Credential.Env)
    } else if a.Credential.Key != "" {
        if f.ReadSecret == nil {
            return nil, &cloud.APIError{Code: "CREDENTIAL_STORE_UNAVAILABLE"}
        }
        var err error
        token, err = f.ReadSecret(ctx, a.Credential)
        if err != nil {
            var api *cloud.APIError
            if errors.As(err, &api) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
                return nil, err
            }
            return nil, &cloud.APIError{Code: "CREDENTIAL_STORE_UNAVAILABLE"}
        }
    }
    if token == "" || strings.ContainsAny(token, "\r\n\x00") {
        return nil, &cloud.APIError{Code: "AUTH_REQUIRED"}
    }
    api := f.newAPI(a, product, token)
    api.account = name
    if a.Credential.Identity != (cloud.Identity{}) {
        actual, standard, pro, err := api.inspectIdentity(ctx)
        if err != nil {
            return nil, err
        }
        if actual != a.Credential.Identity {
            return nil, &cloud.APIError{Code: "IDENTITY_MISMATCH"}
        }
        if product == "standard" && !standard || product == "pro" && pro != nil && !*pro {
            return nil, &cloud.APIError{Code: "PermissionDenied"}
        }
    }
    if a.Credential.Mode == "developer" {
        return &Pro{api}, nil
    }
    if product == "pro" {
        return &WebPro{api}, nil
    }
    return &Standard{api}, nil
}

func (f Factory) newAPI(a cloud.Account, product, token string) *api {
    client := http.Client{Timeout: 20 * time.Second}
    if f.HTTP != nil {
        client = *f.HTTP
    }
    // Even same-host redirects are not part of this API's contract. Never copy
    // Authorization to a redirected URL, and never replay a redirected POST.
    client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
    if client.Timeout == 0 || client.Timeout > 30*time.Second {
        client.Timeout = 20 * time.Second
    }
    api := &api{settings: a, product: product, token: token, http: &client, host: "https://www.autodl.com", prefix: "/api/v1/instance"}
    if product == "pro" {
        api.prefix = "/api/v1/instance/pro"
    }
    if a.Credential.Mode == "developer" {
        api.host, api.prefix = "https://api.autodl.com", "/api/v1/dev/instance/pro"
    }
    return api
}

type api struct {
    account      string
    settings     cloud.Account
    product      string
    token        string
    http         *http.Client
    host, prefix string
}

// Standard uses a web-session token, with ordinary console inventory.
type Standard struct{ *api }

// Pro uses a developer token. The public Pro API has no read-only stock/quote
// endpoint; it deliberately does not implement cloud.Inventory.
type Pro struct{ *api }

type object map[string]json.RawMessage

func text(o object, key string) string {
    var value string
    _ = json.Unmarshal(o[key], &value)
    return value
}

func integer(o object, key string) *int {
    raw := o[key]
    if len(raw) == 0 || string(raw) == "null" {
        return nil
    }
    var number json.Number
    if json.Unmarshal(raw, &number) != nil {
        return nil
    }
    n, err := strconv.ParseInt(number.String(), 10, 64)
    if err != nil || n < 0 {
        return nil
    }
    value := int(n)
    return &value
}

func (a *api) ref(id string) cloud.Ref {
    return cloud.Ref{Provider: "autodl", Product: a.product, Account: a.account, ID: id}
}

func (a *api) check(ref cloud.Ref) error {
    if ref != a.ref(ref.ID) || !validID.MatchString(ref.ID) {
        return &cloud.APIError{Code: "INVALID_INSTANCE"}
    }
    return nil
}

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// request preserves ambiguity on every mutating transport or server failure.
// It exposes only an allowlisted code, never arbitrary provider diagnostics.
func (a *api) request(ctx context.Context, method, path string, body any, mutation bool) (json.RawMessage, error) {
    data, err := json.Marshal(body)
    if err != nil {
        return nil, err
    }
    address := a.host + a.endpoint(path)
    var input io.Reader = bytes.NewReader(data)
    if method == http.MethodGet {
        values := url.Values{}
        for key, value := range body.(map[string]string) {
            values.Set(key, value)
        }
        address += "?" + values.Encode()
        input = nil
    }
    request, err := http.NewRequestWithContext(ctx, method, address, input)
    if err != nil {
        return nil, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    request.Header.Set("Authorization", a.token)
    request.Header.Set("Content-Type", "application/json")
    request.Header.Set("Accept", "application/json")
    request.Header.Set("User-Agent", "loaf")
    if a.settings.Credential.Mode == "session" && a.settings.AppVersion != "" {
        request.Header.Set("appversion", a.settings.AppVersion)
    }
    response, err := a.http.Do(request)
    if err != nil {
        return nil, &cloud.APIError{Code: "TRANSPORT", Transient: true, Unconfirmed: mutation}
    }
    defer response.Body.Close()
    failure := &cloud.APIError{Code: "INVALID_RESPONSE", HTTPStatus: response.StatusCode, Unconfirmed: mutation}
    failure.Transient = response.StatusCode == 429 || response.StatusCode == 408 || response.StatusCode >= 500
    failure.RetryAfter = retryAfter(response.Header.Get("Retry-After"))
    raw, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
    if err != nil || len(raw) > 4*1024*1024 {
        return nil, failure
    }
    var envelope struct {
        Code string          `json:"code"`
        Data json.RawMessage `json:"data"`
    }
    if json.Unmarshal(raw, &envelope) != nil {
        return nil, failure
    }
    if response.StatusCode < 200 || response.StatusCode >= 300 {
        if response.StatusCode == 401 || response.StatusCode == 403 {
            failure.Code = "AUTH_REQUIRED"
            failure.Unconfirmed = false
        }
        return nil, failure
    }
    if envelope.Code != "Success" {
        // Deliberately narrow: no message matching or retrying arbitrary API
        // errors. Unknown failure codes require human inspection, not a charge.
        switch envelope.Code {
        case "NoStock", "GPUStockNotEnough":
            failure.Code = "NO_STOCK"
            failure.NoStock = true
            failure.Unconfirmed = false
        case "TokenExpired", "InvalidToken", "AuthenticationFailed", "AuthorizeFailed":
            failure.Code = "AUTH_REQUIRED"
            failure.Unconfirmed = false
        case "RequestParameterIsWrong", "InvalidParameter", "InsufficientBalance", "PermissionDenied":
            failure.Code = envelope.Code
            failure.Unconfirmed = false
        default:
            failure.Code = "PROVIDER_REJECTED"
        }
        return nil, failure
    }
    if envelope.Data == nil {
        return nil, failure
    }
    return envelope.Data, nil
}

// endpoint follows the web console's explicit subaccount namespace. The three
// shared Pro catalogs are intentionally not rewritten; this is not a generic
// URL transformer and never applies to developer-token requests.
func (a *api) endpoint(path string) string {
    if a.settings.Credential.Mode != "session" || a.settings.Credential.Identity.Kind != "sub" {
        return path
    }
    switch path {
    case "/api/v1/machine_pro/gpu_type", "/api/v1/machine_pro/gpu_spec_config", "/api/v1/machine_pro/get/gpu_spec_config":
        return path
    }
    if path == "/api/v1" {
        return "/api/v1/sub_user"
    }
    return strings.Replace(path, "/api/v1/", "/api/v1/sub_user/", 1)
}

func retryAfter(value string) time.Duration {
    if n, err := strconv.Atoi(value); err == nil && n >= 0 {
        return time.Duration(min(n, 86400)) * time.Second
    }
    if at, err := http.ParseTime(value); err == nil {
        return max(0, min(time.Until(at), 24*time.Hour))
    }
    return 0
}

type listResponse struct {
    List    []object `json:"list"`
    Total   *int     `json:"result_total"`
    MaxPage *int     `json:"max_page"`
}

func (a *api) list(ctx context.Context, limit int) ([]object, bool, error) {
    result := []object{}
    size := min(100, limit)
    for page := 1; len(result) < limit; page++ {
        body := map[string]any{"page_index": page, "page_size": size}
        endpoint := a.prefix + "/list"
        if a.settings.Credential.Mode == "session" {
            endpoint = a.prefix
            body["date_from"], body["date_to"], body["status"], body["charge_type"] = "", "", []string{}, []string{}
        }
        raw, err := a.request(ctx, "POST", endpoint, body, false)
        if err != nil {
            return result, false, err
        }
        var data listResponse
        if json.Unmarshal(raw, &data) != nil || data.List == nil || len(data.List) > size {
            return result, false, &cloud.APIError{Code: "INVALID_RESPONSE"}
        }
        take := min(len(data.List), limit-len(result))
        result = append(result, data.List[:take]...)
        if take < len(data.List) {
            return result, false, nil
        }
        if len(data.List) < size || data.Total != nil && *data.Total <= len(result) || data.MaxPage != nil && page >= *data.MaxPage {
            return result, true, nil
        }
    }
    return result, false, nil
}

func (a *api) instance(o object) (cloud.Instance, error) {
    id := text(o, "uuid")
    if !validID.MatchString(id) {
        return cloud.Instance{}, &cloud.APIError{Code: "INVALID_RESPONSE"}
    }
    native := text(o, "status")
    status := "unknown"
    switch native {
    case "running":
        status = "running"
    case "shutdown", "stopped":
        status = "stopped"
    case "starting", "powering_on", "creating":
        status = "starting"
    case "stopping", "powering_off":
        status = "stopping"
    }
    return cloud.Instance{Ref: a.ref(id), Name: text(o, "machine_alias"), Status: status, NativeStatus: native, Region: text(o, "region_sign"), ObservedAt: time.Now().UTC()}, nil
}

func (a *api) List(ctx context.Context, limit int) (cloud.Page, error) {
    result := cloud.Page{Instances: []cloud.Instance{}}
    if limit < 1 || limit > 1000 {
        return result, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    items, complete, err := a.list(ctx, limit)
    result.Complete, result.Examined = complete, len(items)
    for _, item := range items {
        v, e := a.instance(item)
        if e != nil {
            return result, e
        }
        result.Instances = append(result.Instances, v)
    }
    return result, err
}

func (a *api) listedInstance(ctx context.Context, ref cloud.Ref) (object, error) {
    if err := a.check(ref); err != nil {
        return nil, err
    }
    items, complete, err := a.list(ctx, 1000)
    if err != nil {
        return nil, err
    }
    for _, o := range items {
        if text(o, "uuid") == ref.ID {
            return o, nil
        }
    }
    code := "INSTANCE_NOT_FOUND"
    if !complete {
        code = "INCOMPLETE_LIST"
    }
    return nil, &cloud.APIError{Code: code}
}

func (a *api) snapshot(ctx context.Context, ref cloud.Ref) (object, error) {
    if a.product == "standard" {
        return a.listedInstance(ctx, ref)
    }
    if err := a.check(ref); err != nil {
        return nil, err
    }
    raw, err := a.request(ctx, "GET", a.prefix+"/snapshot", map[string]string{"instance_uuid": ref.ID}, false)
    if err != nil {
        return nil, err
    }
    var o object
    if json.Unmarshal(raw, &o) != nil || o == nil {
        return nil, &cloud.APIError{Code: "INVALID_RESPONSE"}
    }
    o["uuid"], _ = json.Marshal(ref.ID)
    return o, nil
}

func (a *api) Get(ctx context.Context, ref cloud.Ref) (cloud.Instance, error) {
    o, err := a.snapshot(ctx, ref)
    if err != nil {
        return cloud.Instance{}, err
    }
    if a.product == "pro" && a.settings.Credential.Mode == "developer" {
        raw, err := a.request(ctx, "GET", a.prefix+"/status", map[string]string{"instance_uuid": ref.ID}, false)
        if err != nil {
            return cloud.Instance{}, err
        }
        o["status"] = raw
    }
    if a.product == "pro" && a.settings.Credential.Mode == "session" && text(o, "status") == "" {
        o, err = a.listedInstance(ctx, ref)
        if err != nil {
            return cloud.Instance{}, err
        }
    }
    return a.instance(o)
}

func (a *api) Connection(ctx context.Context, ref cloud.Ref) (cloud.Connection, error) {
    var o object
    var err error
    if a.settings.Credential.Mode == "session" {
        // Both web products return SSH credentials in list rows. Pro's
        // hardware/image snapshot deliberately contains no connection fields.
        o, err = a.listedInstance(ctx, ref)
    } else {
        o, err = a.snapshot(ctx, ref)
    }
    if err != nil {
        return cloud.Connection{}, err
    }
    // Some ordinary responses put connection values inside info.
    if raw := o["info"]; len(raw) > 0 {
        var info object
        if json.Unmarshal(raw, &info) == nil {
            for key, value := range info {
                if _, ok := o[key]; !ok {
                    o[key] = value
                }
            }
        }
    }
    c := cloud.Connection{Host: text(o, "proxy_host"), User: "root", Password: text(o, "root_password")}
    if port := integer(o, "ssh_port"); port != nil {
        c.Port = *port
    }
    if c.Host == "" || c.Port == 0 {
        // Parse only the platform's conventional host/port command, not arbitrary
        // SSH options supplied by a server response.
        fields := strings.Fields(text(o, "ssh_command"))
        if len(fields) == 4 && fields[0] == "ssh" && fields[1] == "-p" {
            c.Port, _ = strconv.Atoi(fields[2])
            user, host, ok := strings.Cut(fields[3], "@")
            if ok {
                c.User, c.Host = user, host
            }
        }
    }
    if !hostName.MatchString(c.Host) || !userName.MatchString(c.User) || c.Port < 1 || c.Port > 65535 || strings.ContainsAny(c.Password, "\x00\r\n") {
        return cloud.Connection{}, &cloud.APIError{Code: "INVALID_CONNECTION"}
    }
    return c, nil
}

var hostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
var userName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func (a *api) power(ctx context.Context, ref cloud.Ref, on bool) error {
    if err := a.check(ref); err != nil {
        return err
    }
    body := map[string]string{"instance_uuid": ref.ID}
    op := "/power_off"
    if on {
        op = "/power_on"
        body["payload"] = "gpu"
    }
    _, err := a.request(ctx, "POST", a.prefix+op, body, true)
    return err
}

func (a *api) Start(ctx context.Context, ref cloud.Ref) error { return a.power(ctx, ref, true) }
func (a *api) Stop(ctx context.Context, ref cloud.Ref) error  { return a.power(ctx, ref, false) }

func created(a *api, raw json.RawMessage, err error) (cloud.Ref, error) {
    if err != nil {
        return cloud.Ref{}, err
    }
    var id string
    if json.Unmarshal(raw, &id) != nil {
        var o object
        _ = json.Unmarshal(raw, &o)
        id = text(o, "instance_uuid")
        if id == "" {
            id = text(o, "uuid")
        }
    }
    if !validID.MatchString(id) {
        return cloud.Ref{}, &cloud.APIError{Code: "INVALID_RESPONSE", Unconfirmed: true}
    }
    return a.ref(id), nil
}

func (p *Pro) Create(ctx context.Context, plan cloud.Plan, _ *cloud.Offer) (cloud.Ref, error) {
    image := plan.AutoDL
    if image.GPUSpecUUID == "" || image.Image == "" || image.CUDAFrom == 0 || plan.Requirements.GPUCount > 4 || image.ExpandDataDiskGB != 0 {
        return cloud.Ref{}, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    body := map[string]any{"req_gpu_amount": plan.Requirements.GPUCount, "expand_system_disk_by_gb": image.ExpandSystemDiskGB, "gpu_spec_uuid": image.GPUSpecUUID, "image_uuid": image.Image, "cuda_v_from": image.CUDAFrom}
    if len(plan.Requirements.Regions) > 0 {
        body["data_center_list"] = plan.Requirements.Regions
    }
    raw, err := p.request(ctx, "POST", p.prefix+"/create", body, true)
    return created(p.api, raw, err)
}

func (s *Standard) Create(ctx context.Context, plan cloud.Plan, offer *cloud.Offer) (cloud.Ref, error) {
    if offer == nil || !validID.MatchString(offer.ID) || (plan.AutoDL.Image == "") == (plan.AutoDL.PrivateImageUUID == "") || plan.AutoDL.Image != "" && !validID.MatchString(plan.AutoDL.Image) || plan.AutoDL.PrivateImageUUID != "" && !validID.MatchString(plan.AutoDL.PrivateImageUUID) || plan.AutoDL.ExpandSystemDiskGB != 0 {
        return cloud.Ref{}, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    compute, _, err := s.quote(ctx, plan, offer.ID)
    if err != nil {
        return cloud.Ref{}, err
    }
    if err := checkPrice(compute, plan.Requirements); err != nil {
        return cloud.Ref{}, err
    }
    // Current ordinary-console request shape, independently checked against
    // https://www.autodl.com/assets/index.9040cb9d.js (2026-10-21): base/private
    // image UUIDs, and data-disk bytes. Pro's developer API instead accepts GB.
    body := map[string]any{
        "instance_info": map[string]any{"charge_type": "payg", "expand_data_disk": int64(plan.AutoDL.ExpandDataDiskGB) << 30, "base_image_uuid": plan.AutoDL.Image, "machine_id": offer.ID, "req_gpu_amount": plan.Requirements.GPUCount, "private_image_uuid": plan.AutoDL.PrivateImageUUID, "instance_name": ""},
        "price_info":    map[string]any{"charge_type": "payg", "expand_data_disk": int64(plan.AutoDL.ExpandDataDiskGB) << 30, "machine_id": offer.ID, "num": plan.Requirements.GPUCount},
    }
    raw, err := s.request(ctx, "POST", "/api/v1/order/instance/create/payg", body, true)
    return created(s.api, raw, err)
}

// Search normalizes only fields whose units the adapter knows. Unrecognized
// hardware fields remain unknown rather than guessing a model's specifications.
func (s *Standard) Search(ctx context.Context, plan cloud.Plan, limit int) (cloud.InventoryPage, error) {
    result := cloud.InventoryPage{Offers: []cloud.Offer{}, ObservedAt: time.Now().UTC()}
    if limit < 1 || limit > 1000 {
        return result, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    size := min(100, limit)
    for page := 1; result.Examined < limit; page++ {
        models := append([]string{}, plan.Requirements.GPUModels...)
        regions := append([]string{}, plan.Requirements.Regions...)
        body := map[string]any{"charge_type": "payg", "default_order": true, "gpu_idle_num": plan.Requirements.GPUCount, "gpu_type_name": models, "region_sign_list": regions, "page_index": page, "page_size": size}
        raw, err := s.request(ctx, "POST", "/api/v1/user/machine/list", body, false)
        if err != nil {
            return result, err
        }
        var data listResponse
        if json.Unmarshal(raw, &data) != nil || data.List == nil || len(data.List) > size {
            return result, &cloud.APIError{Code: "INVALID_RESPONSE"}
        }
        take := min(len(data.List), limit-result.Examined)
        result.Examined += take
        for _, o := range data.List[:take] {
            id := text(o, "machine_id")
            if !validID.MatchString(id) {
                return result, &cloud.APIError{Code: "INVALID_RESPONSE"}
            }
            offer := cloud.Offer{ID: id, Model: text(o, "gpu_name"), Vendor: text(o, "chip_corp"), GPUCount: plan.Requirements.GPUCount, Region: text(o, "region_sign"), Source: "autodl:standard:machine/list", ObservedAt: result.ObservedAt}
            if idle := integer(o, "gpu_idle_num"); idle != nil {
                available := *idle >= offer.GPUCount
                offer.Available = &available
            }
            if online := integer(o, "on_line"); online != nil && *online >= 3 && *online <= 6 {
                no := false
                offer.Available = &no
            }
            maxInstances, bound := integer(o, "max_instance_num"), integer(o, "binding_instance_num")
            if maxInstances != nil && bound != nil && *maxInstances <= *bound {
                no := false
                offer.Available = &no
            }
            // The public rental form displays gpu_memory and mem_per_gpu in
            // bytes, and cpu_per_gpu per card. Never confuse host totals with
            // this request's allocation. Missing fields remain unknown.
            offer.GPUMemoryGiB = allocated(o, "gpu_memory", 1, 1<<30)
            offer.CPUs = allocated(o, "cpu_per_gpu", offer.GPUCount, 1)
            offer.MemoryGiB = allocated(o, "mem_per_gpu", offer.GPUCount, 1<<30)
            offer.DiskGiB = allocated(o, "max_instance_disk_size", 1, 1<<30)
            if plan.AutoDL.ExpandDataDiskGB > 0 {
                capacity := allocated(o, "max_data_disk_expand_size", 1, 1<<30)
                if capacity == nil || *capacity < plan.AutoDL.ExpandDataDiskGB {
                    no := false
                    offer.Available = &no
                } else if offer.DiskGiB != nil {
                    *offer.DiskGiB += plan.AutoDL.ExpandDataDiskGB
                }
            }
            eligible := plan.Requirements
            eligible.MaxComputeHourlyPrice = nil
            if len(cloud.Match(offer, eligible)) == 0 {
                offer.ComputeHourlyPrice, offer.StorageDailyPrice, err = s.quote(ctx, plan, id)
                if err != nil {
                    return result, err
                }
            }
            result.Offers = append(result.Offers, offer)
        }
        if take < len(data.List) {
            return result, nil
        }
        if len(data.List) < size || data.Total != nil && *data.Total <= result.Examined || data.MaxPage != nil && page >= *data.MaxPage {
            result.Complete = true
            break
        }
    }
    return result, nil
}

func allocated(o object, key string, count, divisor int) *int {
    raw := integer(o, key)
    if raw == nil || int64(*raw) > int64(^uint64(0)>>1)/int64(count) {
        return nil
    }
    n := *raw * count / divisor
    return &n
}

var _ cloud.Provider = (*Standard)(nil)
var _ cloud.Provider = (*Pro)(nil)
var _ cloud.Inventory = (*Standard)(nil)
