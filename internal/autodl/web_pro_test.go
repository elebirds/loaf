package autodl

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

func webProPlan() cloud.Plan {
    return cloud.Plan{Product: "pro", Requirements: cloud.Requirements{GPUCount: 2, GPUModels: []string{"GPU fixture"}, Regions: []string{"westDC3"}}, AutoDL: cloud.ImageOptions{Image: "base-image", CUDAFrom: 118}, WaitTimeout: "0s", PollInterval: "10s"}
}
func TestWebProCatalogQuoteCreateAndPowerForBothIdentities(t *testing.T) {
    for _, kind := range []string{"main", "sub"} {
        t.Run(kind, func(t *testing.T) {
            prefix := "/api/v1"
            if kind == "sub" {
                prefix += "/sub_user"
            }
            creates, quotes := 0, 0
            f := factory(t, func(r *http.Request) *http.Response {
                if r.URL.Host != "www.autodl.com" {
                    t.Fatal("web token to wrong host")
                }
                var body object
                if r.Method == "POST" {
                    if json.NewDecoder(r.Body).Decode(&body) != nil {
                        t.Fatal("body")
                    }
                }
                switch r.URL.Path {
                case "/api/v1/user/detail":
                    return response(200, `{"code":"Success","data":{"uuid":"user-1","username":"owner"}}`)
                case "/api/v1/sub_user":
                    return response(200, `{"code":"Success","data":{"uuid":"parent-1","id":123,"sub_name":"student@master","roles":{"instance":true,"instance_pro":true}}}`)
                case "/api/v1/machine_pro/gpu_type":
                    return response(200, `{"code":"Success","data":[{"gpu_type":"GPU fixture","gpu_idle_num":4,"gpu_total_number":8}]}`)
                case "/api/v1/machine_pro/gpu_spec_config":
                    return response(200, `{"code":"Success","data":[{"id":21,"gpu_spec_name":"shared","cpu_num_from":8,"cpu_num_to":16,"memory_size_from":34359738368,"memory_size_to":68719476736}]}`)
                case prefix + "/order/price/preview/pro":
                    quotes++
                    var spec map[string]int
                    json.Unmarshal(body["gpu_spec_info"], &spec)
                    if spec["id"] != 21 || *integer(body, "num") != 2 || *integer(body, "expand_system_disk") != 30<<30 {
                        t.Fatal("wrong quote units/body")
                    }
                    return response(200, `{"code":"Success","data":{"discount_list":[{"product_type":"instance_pro","origin_price":2400,"total_reduction":200},{"product_type":"system_disk","origin_price":300,"total_reduction":0}]}}`)
                case prefix + "/order/instance/pro/create/payg":
                    creates++
                    var info, price object
                    json.Unmarshal(body["instance_info"], &info)
                    json.Unmarshal(body["price_info"], &price)
                    if *integer(info, "req_gpu_amount") != 2 || *integer(info, "expand_system_disk") != 30<<30 || text(info, "base_image_uuid") != "base-image" || text(info, "private_image_uuid") != "" || info["start_command"] != nil || info["image_uuid"] != nil || info["gpu_spec_uuid"] != nil {
                        t.Fatal("wrong web order contract")
                    }
                    return response(200, `{"code":"Success","data":{"uuid":"created-web"}}`)
                case prefix + "/instance/pro/snapshot":
                    return response(200, `{"code":"Success","data":{"image":{"image_type":"public"}}}`)
                case prefix + "/instance/pro":
                    return response(200, `{"code":"Success","data":{"list":[{"uuid":"created-web","status":"running","proxy_host":"gpu.example.com","ssh_port":2200,"root_password":"never-public"}],"result_total":1}}`)
                case prefix + "/instance/pro/power_on", prefix + "/instance/pro/power_off":
                    return response(200, `{"code":"Success","data":null}`)
                }
                t.Fatal("unexpected endpoint", r.Method, r.URL)
                return nil
            })
            a := account("standard")
            a.DefaultProduct = "standard"
            username, principalID := "owner", "user-1"
            if kind == "sub" {
                username, principalID = "student@master", "123"
            }
            a.Credential.Identity = cloud.Identity{Kind: kind, ID: principalID, Username: username}
            provider, err := f.Open(context.Background(), "demo", a, "pro")
            if err != nil {
                t.Fatal(err)
            }
            p := provider.(*WebPro)
            request := webProPlan()
            request.Requirements.MaxStorageDailyPrice = &cloud.Money{Amount: "0.30", Currency: "CNY"}
            page, err := p.Search(context.Background(), request, 10)
            if err != nil || !page.Complete || page.Examined != 1 || len(page.Offers) != 1 {
                t.Fatal(page, err)
            }
            offer := page.Offers[0]
            if offer.ComputeHourlyPrice == nil || offer.ComputeHourlyPrice.Amount != "2.200" || offer.StorageDailyPrice.Amount != "0.300" || offer.GPUMemoryGiB != nil || *offer.CPUs != 8 || *offer.MemoryGiB != 32 {
                t.Fatal(offer)
            }
            id, err := p.Create(context.Background(), request, &offer)
            if err != nil || id.Product != "pro" || id.ID != "created-web" || creates != 1 || quotes != 2 {
                t.Fatal(id, err, creates, quotes)
            }
            if _, err := p.Get(context.Background(), id); err != nil {
                t.Fatal(err)
            }
            c, err := p.Connection(context.Background(), id)
            if err != nil || c.Host != "gpu.example.com" || c.Password != "never-public" {
                t.Fatal("connection", err)
            }
            if _, err := p.List(context.Background(), 10); err != nil {
                t.Fatal(err)
            }
            if p.Start(context.Background(), id) != nil || p.Stop(context.Background(), id) != nil {
                t.Fatal("power")
            }
        })
    }
}
func TestWebProNeverAllocatesWhenQuoteChangesOrIsUnknown(t *testing.T) {
    for _, data := range []string{`{"discount_list":[{"product_type":"instance_pro","origin_price":9999,"total_reduction":0}]}`, `{"discount_list":[]}`, `{"discount_list":[{"product_type":"instance_pro","origin_price":100,"total_reduction":101}]}`} {
        calls := 0
        f := factory(t, func(r *http.Request) *http.Response {
            calls++
            if !strings.HasSuffix(r.URL.Path, "/price/preview/pro") {
                t.Fatal("allocated")
            }
            return response(200, `{"code":"Success","data":`+data+`}`)
        })
        a := account("standard")
        provider, err := f.Open(context.Background(), "demo", a, "pro")
        if err != nil {
            t.Fatal(err)
        }
        plan := webProPlan()
        cap, _ := cloud.ParseMoney("3.00", "CNY")
        plan.Requirements.MaxComputeHourlyPrice = &cap
        yes := true
        offer := cloud.Offer{ID: webProID(21, "GPU fixture", "westDC3"), Model: "GPU fixture", Region: "westDC3", GPUCount: 2, Available: &yes}
        _, err = provider.Create(context.Background(), plan, &offer)
        var api *cloud.APIError
        if !errors.As(err, &api) || api.Unconfirmed || calls != 1 {
            t.Fatal(err, calls)
        }
    }
}
func TestWebProRejectsMissingPermissionsAndPreservesUnknownHardware(t *testing.T) {
    f := factory(t, func(r *http.Request) *http.Response {
        return response(200, `{"code":"Success","data":{"uuid":"parent-1","id":123,"sub_name":"student@master","roles":{"instance":true,"instance_pro":"no_permission"}}}`)
    })
    a := account("standard")
    a.Credential.Identity = cloud.Identity{Kind: "sub", ID: "123", Username: "student@master"}
    _, err := f.Open(context.Background(), "demo", a, "pro")
    var api *cloud.APIError
    if !errors.As(err, &api) || api.Code != "PermissionDenied" {
        t.Fatal(err)
    }
}

func TestWebProDoesNotQuoteIneligibleOrOutOfStockSpecs(t *testing.T) {
    for _, noStock := range []bool{true, false} {
        f := factory(t, func(r *http.Request) *http.Response {
            switch r.URL.Path {
            case "/api/v1/machine_pro/gpu_type":
                idle := "0"
                if !noStock {
                    idle = "4"
                }
                return response(200, `{"code":"Success","data":[{"gpu_type":"GPU fixture","gpu_idle_num":`+idle+`}]}`)
            case "/api/v1/machine_pro/gpu_spec_config":
                return response(200, `{"code":"Success","data":[{"id":21}]}`)
            }
            t.Fatal("quoted a known ineligible candidate", r.URL)
            return nil
        })
        provider, err := f.Open(context.Background(), "demo", account("standard"), "pro")
        if err != nil {
            t.Fatal(err)
        }
        plan := webProPlan()
        if !noStock {
            plan.Requirements.MinGPUMemoryGiB = 24
        }
        page, err := provider.(cloud.Inventory).Search(context.Background(), plan, 10)
        if err != nil || !page.Complete || page.Examined != 1 {
            t.Fatal(page, err)
        }
        result := cloud.Select(page, plan)
        if len(result.Matches) != 0 || len(result.Rejected) != 1 {
            t.Fatal(result)
        }
    }
}

func TestWebProAcceptsEightGPUsWithoutDeveloperLimit(t *testing.T) {
    creates := 0
    f := factory(t, func(r *http.Request) *http.Response {
        var body object
        json.NewDecoder(r.Body).Decode(&body)
        if strings.HasSuffix(r.URL.Path, "/price/preview/pro") {
            if n := integer(body, "num"); n == nil || *n != 8 {
                t.Fatal("wrong quote card count")
            }
            return response(200, `{"code":"Success","data":{"discount_list":[{"product_type":"instance_pro","origin_price":8000,"total_reduction":0}]}}`)
        }
        var info object
        json.Unmarshal(body["instance_info"], &info)
        if n := integer(info, "req_gpu_amount"); n == nil || *n != 8 {
            t.Fatal("wrong allocation card count")
        }
        creates++
        return response(200, `{"code":"Success","data":"eight-gpu-fixture"}`)
    })
    provider, err := f.Open(context.Background(), "demo", account("standard"), "pro")
    if err != nil {
        t.Fatal(err)
    }
    plan := webProPlan()
    plan.Requirements.GPUCount = 8
    yes := true
    offer := cloud.Offer{ID: webProID(21, "GPU fixture", "westDC3"), Model: "GPU fixture", Region: "westDC3", GPUCount: 8, Available: &yes}
    if _, err := provider.Create(context.Background(), plan, &offer); err != nil || creates != 1 {
        t.Fatal(err, creates)
    }
}

func TestWebProWithoutRegionQueriesBothConsoleDataCenters(t *testing.T) {
    regions := []string{}
    f := factory(t, func(r *http.Request) *http.Response {
        if r.URL.Path != "/api/v1/machine_pro/gpu_type" {
            t.Fatal("unexpected request", r.URL.Path)
        }
        var body struct {
            Regions []string `json:"data_center_list"`
        }
        if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Regions) != 1 {
            t.Fatal("empty or aggregated region selection")
        }
        regions = append(regions, body.Regions[0])
        return response(200, `{"code":"Success","data":[]}`)
    })
    p, err := f.Open(context.Background(), "demo", account("standard"), "pro")
    if err != nil {
        t.Fatal(err)
    }
    plan := webProPlan()
    plan.Requirements.Regions = nil
    page, err := p.(cloud.Inventory).Search(context.Background(), plan, 10)
    if err != nil || !page.Complete || strings.Join(regions, ",") != "westDC3,beijingDC2" {
        t.Fatal(page, regions, err)
    }
}
