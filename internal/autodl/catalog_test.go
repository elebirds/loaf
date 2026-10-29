package autodl

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

func TestRegionCatalogRetainsCanonicalIDsAndDataCenterGroups(t *testing.T) {
    f := factory(t, func(r *http.Request) *http.Response {
        if r.URL.Path != "/api/v1/common" || r.Method != "GET" {
            t.Fatal(r.Method, r.URL.Path)
        }
        return response(200, `{"code":"Success","data":{"region_list":[
            {"region_sign":"west-B","region_name":"西北B区","data_center":"westDC3"},
            {"region_sign":"bj-B1","region_name":"北京B区","data_center":"beijingDC2"},
            {"region_sign":"west-C","region_name":"西北B区","data_center":"westDC3"},
            {"region_sign":"west-D","region_name":"西北B区","data_center":"westDC3"},
            {"region_sign":"west-E","region_name":"西北B区","data_center":"westDC3"}
        ]}}`)
    })
    p, err := f.Open(context.Background(), "demo", account("standard"), "standard")
    if err != nil {
        t.Fatal(err)
    }
    got, err := p.(cloud.Catalog).Catalog(context.Background(), cloud.CatalogInput{Category: "region", Limit: 100})
    want := []cloud.CatalogItem{{ID: "west-B", Name: "西北B区", DataCenter: "westDC3"}, {ID: "bj-B1", Name: "北京B区", DataCenter: "beijingDC2"}, {ID: "west-C", Name: "西北B区", DataCenter: "westDC3"}, {ID: "west-D", Name: "西北B区", DataCenter: "westDC3"}, {ID: "west-E", Name: "西北B区", DataCenter: "westDC3"}}
    if err != nil || !got.Complete || got.Examined != 5 || !reflect.DeepEqual(got.Items, want) {
        t.Fatal(got, err)
    }
}

func TestRegionCatalogReportsVisibilityWithoutHidingEnterpriseRegions(t *testing.T) {
    f := factory(t, func(r *http.Request) *http.Response {
        if r.URL.Path != "/api/v1/common" || r.Method != "GET" {
            t.Fatal(r.Method, r.URL.Path)
        }
        return response(200, `{"code":"Success","data":{"region_list":[
            {"region_sign":"west-B","region_name":"西北B区","visible":"all","used_for":"all"},
            {"region_sign":"west-X","region_name":"西北企业区","visible":"enterprise","used_for":"all"},
            {"region_sign":"deploy","region_name":"Deployment","visible":"all","used_for":"deploy"},
            {"region_sign":"unknown","region_name":"Unknown"}
        ]}}`)
    })
    p, err := f.Open(context.Background(), "demo", account("standard"), "standard")
    if err != nil {
        t.Fatal(err)
    }
    page, err := p.(cloud.Catalog).Catalog(context.Background(), cloud.CatalogInput{Category: "region", Limit: 100})
    if err != nil || !page.Complete || len(page.Items) != 4 {
        t.Fatal(page, err)
    }
    public, restricted := true, false
    want := []*bool{&public, &restricted, &restricted, nil}
    for i, item := range page.Items {
        if !reflect.DeepEqual(item.Public, want[i]) {
            t.Fatal(item, want[i])
        }
    }
    encoded, err := json.Marshal(page)
    if err != nil || !strings.Contains(string(encoded), `"id":"west-X","name":"西北企业区","public":false`) {
        t.Fatal(string(encoded), err)
    }
}

func TestConsoleCatalogContractsAndSafeImageTree(t *testing.T) {
    f := factory(t, func(r *http.Request) *http.Response {
        switch r.URL.Path {
        case "/api/v1/machine/gpu_type":
            if r.Method != "GET" || r.ContentLength > 0 {
                t.Fatal(r.Method, r.ContentLength)
            }
            return response(200, `{"code":"Success","data":[{"gpu_name":"RTX 4090","chip_corp":"nvidia"},{"gpu_name":"RTX 4090D","chip_corp":"nvidia"}]}`)
        case "/api/v1/common":
            return response(200, `{"code":"Success","data":{"region_list":[{"region_sign":"west","region_name":"West"}]}}`)
        case "/api/v1/image/all":
            var body struct {
                CPU    []string `json:"cpu_arch"`
                Vendor []string `json:"chip_corp"`
            }
            if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.CPU) != 1 || body.CPU[0] != "x86" {
                t.Fatal(body)
            }
            return response(200, `{"code":"Success","data":[{"label_name":"PyTorch","children":[{"label_name":"2.6","children":[{"label_name":"3.12","children":[{"label_name":{"uuid":"image-1","v":"12.6","chip_corp":"nvidia","cpu_arch":"x86"}}]}]}]}]}`)
        case "/api/v1/image/private/get":
            return response(200, `{"code":"Success","data":[{"image_uuid":"private-1","name":"Saved environment","cuda_v":"12.6","root_password":"never-public"}]}`)
        default:
            t.Fatal(r.URL.Path)
            return nil
        }
    })
    p, err := f.Open(context.Background(), "demo", account("standard"), "standard")
    if err != nil {
        t.Fatal(err)
    }
    catalog := p.(cloud.Catalog)
    for _, in := range []cloud.CatalogInput{{Category: "gpu", Query: "4090", Limit: 1}, {Category: "region", Limit: 100}, {Category: "image", Kind: "base", Architecture: "x86", Limit: 100}, {Category: "image", Kind: "private", Limit: 100}} {
        page, err := catalog.Catalog(context.Background(), in)
        if err != nil || len(page.Items) != 1 {
            t.Fatal(in, page, err)
        }
        if in.Category == "gpu" && page.Complete {
            t.Fatal("truncated catalog claimed complete")
        }
        if in.Kind == "base" && page.Items[0].Python != "3.12" {
            t.Fatal(page)
        }
        encoded, _ := json.Marshal(page)
        if strings.Contains(string(encoded), "never-public") {
            t.Fatal("private field escaped allowlist")
        }
    }
}

func TestCreationRequoteFailureIsProvenReadOnly(t *testing.T) {
    for _, product := range []string{"standard", "pro"} {
        t.Run(product, func(t *testing.T) {
            calls := 0
            f := factory(t, func(r *http.Request) *http.Response {
                calls++
                if !strings.Contains(r.URL.Path, "price/preview") {
                    t.Fatal("issued mutation after read failure", r.URL.Path)
                }
                return response(503, `{"code":"Failed","data":null}`)
            })
            p, err := f.Open(context.Background(), "demo", account("standard"), product)
            if err != nil {
                t.Fatal(err)
            }
            plan := cloud.Plan{Product: "standard", Requirements: cloud.Requirements{GPUCount: 1}, AutoDL: cloud.ImageOptions{Image: "base"}}
            offer := &cloud.Offer{ID: "host"}
            if product == "pro" {
                plan = webProPlan()
                offer = &cloud.Offer{ID: webProID(21, "GPU fixture", "westDC3"), Model: "GPU fixture", Region: "westDC3"}
            }
            _, err = p.Create(context.Background(), plan, offer)
            var api *cloud.APIError
            if !errors.As(err, &api) || !api.BeforeMutation || !api.Transient || api.Unconfirmed || calls != 1 {
                t.Fatal(err, calls)
            }
        })
    }
}

func TestDailyStorageIsRequotedBeforeMutation(t *testing.T) {
    created := 0
    f := factory(t, func(r *http.Request) *http.Response {
        if strings.HasSuffix(r.URL.Path, "price/preview") {
            return response(200, `{"code":"Success","data":{"discount_list":[{"product_type":"instance","origin_price":1500,"total_reduction":0},{"product_type":"data_disk","origin_price":2000,"total_reduction":0}]}}`)
        }
        created++
        return response(200, `{"code":"Success","data":{"uuid":"unexpected"}}`)
    })
    p, err := f.Open(context.Background(), "demo", account("standard"), "standard")
    if err != nil {
        t.Fatal(err)
    }
    in := plan()
    in.Product = "standard"
    in.AutoDL = cloud.ImageOptions{Image: "image"}
    in.Requirements.MaxStorageDailyPrice = &cloud.Money{Amount: "1.00", Currency: "CNY"}
    _, err = p.Create(context.Background(), in, &cloud.Offer{ID: "host"})
    if err == nil || created != 0 {
        t.Fatal(err, created)
    }
}

func TestImageCatalogEmptyBranchesNumericCUDAAndProScope(t *testing.T) {
    f := factory(t, func(r *http.Request) *http.Response {
        switch r.URL.Path {
        case "/api/v1/image/all":
            return response(200, `{"code":"Success","data":[{"label_name":"Empty","children":[]},{"label_name":"PyTorch","children":[{"label_name":"2.6","children":[{"label_name":"3.12","children":[{"label_name":{"uuid":"image-1","v":12.6}}]}]}]}]}`)
        case "/api/v1/machine_pro/gpu_type":
            return response(200, `{"code":"Success","data":[{"gpu_type":"RTX 4090"},{"gpu_type":"RTX 5090"}]}`)
        default:
            t.Fatal(r.URL.Path)
            return nil
        }
    })
    p, err := f.Open(context.Background(), "demo", account("standard"), "pro")
    if err != nil {
        t.Fatal(err)
    }
    catalog := p.(cloud.Catalog)
    page, err := catalog.Catalog(context.Background(), cloud.CatalogInput{Category: "image", Limit: 100})
    if err != nil || len(page.Items) != 1 || page.Items[0].CUDA != "12.6" {
        t.Fatal(page, err)
    }
    page, err = catalog.Catalog(context.Background(), cloud.CatalogInput{Category: "gpu", Query: "4090", Limit: 1})
    if err != nil || len(page.Items) != 1 || page.Items[0].ID != "RTX 4090" {
        t.Fatal(page, err)
    }
    page, err = catalog.Catalog(context.Background(), cloud.CatalogInput{Category: "region", Query: "west", Limit: 1})
    if err != nil || len(page.Items) != 1 || page.Examined != 2 || page.Complete {
        t.Fatal(page, err)
    }
}
