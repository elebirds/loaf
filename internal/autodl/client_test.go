package autodl

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, data string) *http.Response {
    return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}
}

func factory(t *testing.T, handle func(*http.Request) *http.Response) Factory {
    t.Helper()
    return Factory{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
        if r.Header.Get("Authorization") != "fixture-token" {
            t.Fatal("missing/wrong control-plane credential")
        }
        return handle(r), nil
    })}, LookupEnv: func(name string) (string, bool) {
        if name != "TEST_TOKEN" {
            t.Fatal(name)
        }
        return "fixture-token", true
    }}
}

func account(product string) cloud.Account {
    return cloud.Account{Provider: "autodl", DefaultProduct: product, Credential: cloud.CredentialRef{Mode: map[string]string{"standard": "session", "pro": "developer"}[product], Env: "TEST_TOKEN"}}
}
func ref(product, id string) cloud.Ref {
    return cloud.Ref{Provider: "autodl", Product: product, Account: "demo", ID: id}
}
func plan() cloud.Plan {
    return cloud.Plan{Requirements: cloud.Requirements{GPUCount: 2}, AutoDL: cloud.ImageOptions{Image: "image-fixture", GPUSpecUUID: "spec", CUDAFrom: 118}, WaitTimeout: "0s", PollInterval: "10s"}
}

func TestProductsUseSeparateHostsAndContracts(t *testing.T) {
    for _, product := range []string{"standard", "pro"} {
        calls := []string{}
        f := factory(t, func(r *http.Request) *http.Response {
            host := map[string]string{"standard": "www.autodl.com", "pro": "api.autodl.com"}[product]
            if r.URL.Host != host {
                t.Fatal(r.URL)
            }
            calls = append(calls, r.Method+" "+r.URL.Path)
            if strings.HasSuffix(r.URL.Path, "/status") {
                if r.Method != "GET" || r.URL.Query().Get("instance_uuid") != "fixture" || r.ContentLength > 0 {
                    t.Fatal(r.Method, r.URL, r.ContentLength)
                }
                return response(200, `{"code":"Success","data":"shutdown"}`)
            }
            if strings.HasSuffix(r.URL.Path, "/snapshot") {
                return response(200, `{"code":"Success","data":{"proxy_host":"gpu.example.com","ssh_port":2200,"root_password":"SSH-secret","jupyter_token":"never-expose"}}`)
            }
            if strings.HasSuffix(r.URL.Path, "/power_on") || strings.HasSuffix(r.URL.Path, "/power_off") {
                if r.Method != "POST" {
                    t.Fatal(r.Method)
                }
                var body map[string]string
                _ = json.NewDecoder(r.Body).Decode(&body)
                if body["instance_uuid"] != "fixture" {
                    t.Fatal(body)
                }
                if strings.HasSuffix(r.URL.Path, "/power_on") && body["payload"] != "gpu" {
                    t.Fatal(body)
                }
                return response(200, `{"code":"Success","data":null}`)
            }
            return response(200, `{"code":"Success","data":{"list":[{"uuid":"fixture","status":"shutdown","ssh_command":"ssh -p 2200 root@gpu.example.com","root_password":"SSH-secret","jupyter_token":"never-expose"}],"result_total":1}}`)
        })
        p, err := f.Open(context.Background(), "demo", account(product), product)
        if err != nil {
            t.Fatal(err)
        }
        v, err := p.Get(context.Background(), ref(product, "fixture"))
        if err != nil || v.Status != "stopped" {
            t.Fatal(v, err)
        }
        c, err := p.Connection(context.Background(), ref(product, "fixture"))
        if err != nil || c.Host != "gpu.example.com" || c.Port != 2200 || c.Password != "SSH-secret" {
            t.Fatal(c, err)
        }
        public, _ := json.Marshal([]any{v, c})
        if strings.Contains(string(public), "secret") || strings.Contains(string(public), "never-expose") {
            t.Fatal("credential leak")
        }
        if p.Start(context.Background(), ref(product, "fixture")) != nil || p.Stop(context.Background(), ref(product, "fixture")) != nil {
            t.Fatal(calls)
        }
        if _, ok := p.(cloud.Inventory); ok != (product == "standard") {
            t.Fatal("Pro incorrectly claims read-only inventory")
        }
    }
}

func TestCreateUsesProductBodiesAndCorrectDiskUnits(t *testing.T) {
    for _, product := range []string{"standard", "pro"} {
        f := factory(t, func(r *http.Request) *http.Response {
            if r.URL.Path == "/api/v1/order/price/preview" {
                return response(200, `{"code":"Success","data":{"discount_list":[{"product_type":"instance","origin_price":1000,"total_reduction":0}]}}`)
            }
            var body map[string]any
            if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
                t.Fatal(err)
            }
            if product == "standard" {
                if r.URL.Path != "/api/v1/order/instance/create/payg" {
                    t.Fatal(r.URL)
                }
                info, price := body["instance_info"].(map[string]any), body["price_info"].(map[string]any)
                if info["req_gpu_amount"] != float64(2) || info["expand_data_disk"] != float64(32<<30) || price["expand_data_disk"] != float64(32<<30) || price["num"] != float64(2) || info["machine_id"] != "machine" {
                    t.Fatal(body)
                }
                if info["base_image_uuid"] != "image-fixture" || info["image"] != nil {
                    t.Fatal("outdated ordinary image contract", body)
                }
            } else {
                if r.URL.Path != "/api/v1/dev/instance/pro/create" || body["req_gpu_amount"] != float64(2) || body["cuda_v_from"] != float64(118) || body["gpu_spec_uuid"] != "spec" || body["image_uuid"] != "image-fixture" || body["expand_system_disk_by_gb"] != float64(32) || body["start_command"] != nil {
                    t.Fatal(body, r.URL)
                }
            }
            return response(200, `{"code":"Success","data":"new-instance"}`)
        })
        p, err := f.Open(context.Background(), "demo", account(product), product)
        if err != nil {
            t.Fatal(err)
        }
        plan := plan()
        if product == "standard" {
            plan.AutoDL.ExpandDataDiskGB = 32
            plan.AutoDL.GPUSpecUUID = ""
            plan.AutoDL.CUDAFrom = 0
        } else {
            plan.AutoDL.ExpandSystemDiskGB = 32
        }
        created, err := p.Create(context.Background(), plan, &cloud.Offer{ID: "machine"})
        if err != nil || created != ref(product, "new-instance") {
            t.Fatal(created, err)
        }
    }
}

func TestOrdinaryPrivateImageUsesItsOwnUUID(t *testing.T) {
    calls := 0
    f := factory(t, func(r *http.Request) *http.Response {
        if r.URL.Path == "/api/v1/order/price/preview" {
            return response(200, `{"code":"Success","data":{"discount_list":[{"product_type":"instance","origin_price":1000,"total_reduction":0}]}}`)
        }
        calls++
        var body map[string]map[string]any
        if json.NewDecoder(r.Body).Decode(&body) != nil || body["instance_info"]["base_image_uuid"] != "" || body["instance_info"]["private_image_uuid"] != "image-private" {
            t.Fatal(body)
        }
        return response(200, `{"code":"Success","data":"created"}`)
    })
    p, _ := f.Open(context.Background(), "demo", account("standard"), "standard")
    q := plan()
    q.AutoDL.Image = ""
    q.AutoDL.PrivateImageUUID = "image-private"
    if _, err := p.Create(context.Background(), q, &cloud.Offer{ID: "m"}); err != nil || calls != 1 {
        t.Fatal(err, calls)
    }
    q.AutoDL.Image = "image-base"
    if _, err := p.Create(context.Background(), q, &cloud.Offer{ID: "m"}); err == nil || calls != 1 {
        t.Fatal("ambiguous image selector sent a paid request")
    }
}

func TestOrdinaryHardwareIsPerCardAndQuoteIsWholeInstance(t *testing.T) {
    f := factory(t, func(r *http.Request) *http.Response {
        if r.URL.Path == "/api/v1/order/price/preview" {
            var body object
            json.NewDecoder(r.Body).Decode(&body)
            if *integer(body, "num") != 2 || text(body, "machine_id") != "m" {
                t.Fatal("quote body")
            }
            return response(200, `{"code":"Success","data":{"discount_list":[{"product_type":"instance","origin_price":2502,"total_reduction":200},{"product_type":"data_disk","origin_price":0,"total_reduction":0}]}}`)
        }
        if r.URL.Path != "/api/v1/user/machine/list" {
            t.Fatal(r.URL)
        }
        return response(200, fmt.Sprintf(`{"code":"Success","data":{"list":[{"machine_id":"m","gpu_name":"RTX fixture","chip_corp":"nvidia","gpu_idle_num":2,"gpu_memory":%d,"cpu_per_gpu":8,"mem_per_gpu":%d,"max_instance_disk_size":%d,"max_instance_num":4,"binding_instance_num":3,"on_line":1,"payg_price":1,"region_sign":"west"}]}}`, 24<<30, 32<<30, 100<<30))
    })
    p, err := f.Open(context.Background(), "demo", account("standard"), "standard")
    if err != nil {
        t.Fatal(err)
    }
    page, err := p.(cloud.Inventory).Search(context.Background(), plan(), 100)
    if err != nil || len(page.Offers) != 1 {
        t.Fatal(page, err)
    }
    o := page.Offers[0]
    if o.Model != "RTX fixture" || *o.GPUMemoryGiB != 24 || *o.CPUs != 16 || *o.MemoryGiB != 64 || *o.DiskGiB != 100 || !*o.Available || o.ComputeHourlyPrice.Amount != "2.302" {
        t.Fatal(o)
    }
}

func TestBoundedPaginationKeepsPageSizeStable(t *testing.T) {
    requests := 0
    f := factory(t, func(r *http.Request) *http.Response {
        requests++
        var body struct{ Page, Size int }
        var raw map[string]int
        _ = json.NewDecoder(r.Body).Decode(&raw)
        body.Page, body.Size = raw["page_index"], raw["page_size"]
        if body.Size != 100 || body.Page != requests {
            t.Fatal(body)
        }
        rows := []string{}
        for i := 0; i < 100; i++ {
            rows = append(rows, fmt.Sprintf(`{"uuid":"i-%d","status":"running"}`, 100*(requests-1)+i))
        }
        return response(200, `{"code":"Success","data":{"list":[`+strings.Join(rows, ",")+`],"result_total":200}}`)
    })
    p, err := f.Open(context.Background(), "demo", account("pro"), "pro")
    if err != nil {
        t.Fatal(err)
    }
    page, err := p.List(context.Background(), 150)
    if err != nil || len(page.Instances) != 150 || page.Examined != 150 || page.Complete || page.Instances[100].Ref.ID != "i-100" {
        t.Fatal(page, err)
    }
}

func TestFailuresDoNotExposeBodiesOrReplayMutations(t *testing.T) {
    for _, tc := range []struct {
        status               int
        body                 string
        unconfirmed, noStock bool
    }{
        {500, `{"code":"Success","data":"made"}`, true, false},
        {200, `{"code":"Success","data":{}}`, true, false},
        {200, `{"code":"SomethingUnexpected","msg":"fixture-token SSH-secret"}`, true, false},
        {200, `{"code":"NoStock","data":null}`, false, true},
        {401, `{"code":"InvalidToken","msg":"fixture-token"}`, false, false},
    } {
        calls := 0
        f := factory(t, func(*http.Request) *http.Response { calls++; return response(tc.status, tc.body) })
        p, err := f.Open(context.Background(), "demo", account("pro"), "pro")
        if err != nil {
            t.Fatal(err)
        }
        _, err = p.Create(context.Background(), plan(), nil)
        var api *cloud.APIError
        if !errors.As(err, &api) || api.Unconfirmed != tc.unconfirmed || api.NoStock != tc.noStock || calls != 1 || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "fixture-token") {
            t.Fatal(tc, err, calls)
        }
    }
}

func TestRedirectAndTransportAmbiguity(t *testing.T) {
    calls := 0
    f := factory(t, func(*http.Request) *http.Response {
        calls++
        r := response(307, "")
        r.Header.Set("Location", "https://attacker.invalid/")
        return r
    })
    p, _ := f.Open(context.Background(), "demo", account("pro"), "pro")
    if _, err := p.Create(context.Background(), plan(), nil); err == nil || calls != 1 {
        t.Fatal(err, calls)
    }
    f.HTTP.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture-token must not leak") })
    p, _ = f.Open(context.Background(), "demo", account("pro"), "pro")
    _, err := p.Create(context.Background(), plan(), nil)
    var api *cloud.APIError
    if !errors.As(err, &api) || !api.Unconfirmed || strings.Contains(err.Error(), "fixture-token") {
        t.Fatal(err)
    }
    if d := retryAfter("120"); d != 2*time.Minute {
        t.Fatal(d)
    }
}
