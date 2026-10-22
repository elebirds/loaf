package autodl

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

type cloneFixture struct {
    sourceStatus, sourceRegion, image, price, imageType, private string
    clones, creates, snapshots                                   int
    failure                                                      string
    client                                                       *Standard
}

func newCloneFixture(t *testing.T, sub bool) *cloneFixture {
    t.Helper()
    x := &cloneFixture{sourceStatus: "shutdown", sourceRegion: "west-D", image: "base-original", price: "2780", imageType: "public"}
    prefix := "/api/v1/"
    a := account("standard")
    if sub {
        prefix = "/api/v1/sub_user/"
        a.Credential.Identity = cloud.Identity{Kind: "sub", ID: "123", Username: "student@master"}
    }
    f := factory(t, func(r *http.Request) *http.Response {
        if !strings.HasPrefix(r.URL.Path, prefix) {
            t.Fatal("wrong account namespace", r.URL.Path)
        }
        path := strings.TrimPrefix(r.URL.Path, prefix)
        switch path {
        case "instance":
            return response(200, fmt.Sprintf(`{"code":"Success","data":{"list":[{"uuid":"source","status":%q,"region_sign":%q,"expand_data_disk_size":161061273600,"private_image_uuid":%q,"reproduction_uuid":"","reproduction_id":0,"root_password":"never-public-password","jupyter_token":"never-public-jupyter"}],"result_total":1}}`, x.sourceStatus, x.sourceRegion, x.private))
        case "instance/snapshot":
            x.snapshots++
            if r.Method != "GET" || r.URL.Query().Get("instance_uuid") != "source" {
                t.Fatal(r.Method, r.URL)
            }
            return response(200, fmt.Sprintf(`{"code":"Success","data":{"image":{"image_name":%q,"image_type":%q,"chip_corp":"nvidia","cpu_arch":"x86","cuda_version":"12.8"},"cg_application_uuid":"","cg_application_info":{"private":"never-public-app"}}}`, x.image, x.imageType))
        case "user/machine/list":
            var in map[string]any
            if json.NewDecoder(r.Body).Decode(&in) != nil || fmt.Sprint(in["region_sign_list"]) != "[west-D]" || fmt.Sprint(in["cpu_arch"]) != "[x86]" {
                t.Fatal(in)
            }
            return response(200, `{"code":"Success","data":{"list":[{"machine_id":"machine","gpu_name":"RTX 5090","chip_corp":"nvidia","cpu_arch":"x86","highest_cuda_version":"13.2","region_sign":"west-D","gpu_idle_num":1,"gpu_memory":34359738368,"max_instance_disk_size":53687091200,"max_data_disk_expand_size":536870912000},{"machine_id":"old-driver","gpu_name":"RTX 5090","chip_corp":"nvidia","cpu_arch":"x86","highest_cuda_version":"12.7","region_sign":"west-D","gpu_idle_num":1},{"machine_id":"other-arch","gpu_name":"RTX 5090","chip_corp":"nvidia","cpu_arch":"arm","highest_cuda_version":"13.2","region_sign":"west-D","gpu_idle_num":1}],"result_total":3}}`)
        case "order/price/preview":
            return response(200, `{"code":"Success","data":{"discount_list":[{"product_type":"instance","origin_price":`+x.price+`,"total_reduction":0},{"product_type":"data_disk","origin_price":1000,"total_reduction":0}]}}`)
        case "order/instance/create/payg":
            x.creates++
            t.Fatal("clone fell back to ordinary creation")
        case "order/instance/clone/payg":
            x.clones++
            var in struct {
                Source string         `json:"instance_uuid"`
                Info   map[string]any `json:"instance_info"`
                Price  map[string]any `json:"price_info"`
            }
            if json.NewDecoder(r.Body).Decode(&in) != nil || in.Source != "source" || in.Info["copy_data_disk_after_clone"] != true || in.Info["keep_src_user_service_address_after_clone"] != false || in.Info["expand_data_disk"] != float64(150<<30) || in.Price["expand_data_disk"] != float64(150<<30) || in.Info["req_gpu_amount"] != float64(1) || in.Info["machine_id"] != "machine" {
                t.Fatal(in)
            }
            wantBase := "base-original"
            if x.imageType != "public" {
                wantBase = ""
            }
            if in.Info["base_image_uuid"] != wantBase || in.Info["private_image_uuid"] != x.private || in.Info["cg_application_info"].(map[string]any)["private"] != "never-public-app" {
                t.Fatal("incorrect inherited image/application")
            }
            if x.failure == "drop" {
                return response(503, `{"code":"InternalError","data":null}`)
            }
            if x.failure == "same" {
                return response(200, `{"code":"Success","data":"source"}`)
            }
            return response(200, `{"code":"Success","data":"destination"}`)
        default:
            t.Fatal("unexpected route", r.Method, r.URL.Path)
        }
        return response(400, `{"code":"InvalidParameter","data":null}`)
    })
    api := f.newAPI(a, "standard", "fixture-token")
    api.account = "demo"
    x.client = &Standard{api}
    return x
}

func (x *cloneFixture) plan(t *testing.T) cloud.Plan {
    t.Helper()
    source, err := x.client.CloneSource(context.Background(), x.client.ref("source"))
    if err != nil {
        t.Fatal(err)
    }
    plan := cloud.Plan{Product: "standard", Requirements: cloud.Requirements{GPUCount: 1, MaxComputeHourlyPrice: &cloud.Money{Amount: "3.00", Currency: "CNY"}}, AutoDL: cloud.ImageOptions{ExpandDataDiskGB: 150}}
    plan, err = cloud.PrepareClone(plan, source)
    if err != nil {
        t.Fatal(err)
    }
    return plan
}

func cloneOffer() *cloud.Offer {
    yes := true
    return &cloud.Offer{ID: "machine", Region: "west-D", Vendor: "nvidia", GPUCount: 1, Available: &yes, ComputeHourlyPrice: &cloud.Money{Amount: "2.78", Currency: "CNY"}}
}

func TestStandardCloneContractMainAndSub(t *testing.T) {
    for _, sub := range []bool{false, true} {
        for _, private := range []bool{false, true} {
            t.Run(fmt.Sprintf("sub=%v/private=%v", sub, private), func(t *testing.T) {
                x := newCloneFixture(t, sub)
                if private {
                    x.imageType = "private"
                    x.private = "private-original"
                }
                plan := x.plan(t)
                page, err := x.client.Search(context.Background(), plan, 100)
                selected := cloud.Select(page, plan)
                if err != nil || len(selected.Matches) != 1 || selected.Matches[0].ID != "machine" {
                    t.Fatal(selected, err)
                }
                got, err := x.client.Clone(context.Background(), plan, &selected.Matches[0])
                if err != nil || got.ID != "destination" || x.clones != 1 || x.creates != 0 || x.snapshots != 2 {
                    t.Fatal(got, err, x)
                }
                data, _ := json.Marshal([]any{plan, got, selected})
                if strings.Contains(string(data), "never-public") {
                    t.Fatal("private source data leaked")
                }
            })
        }
    }
}

func TestCloneRechecksSourceRegionImageStateAndPrice(t *testing.T) {
    for _, tc := range []struct {
        name, code string
        change     func(*cloneFixture, *cloud.Plan, *cloud.Offer)
    }{
        {"wrong destination", "BAD_REQUEST", func(_ *cloneFixture, _ *cloud.Plan, o *cloud.Offer) { o.Region = "west-X" }},
        {"source moved", "CLONE_SOURCE_CHANGED", func(x *cloneFixture, _ *cloud.Plan, _ *cloud.Offer) { x.sourceRegion = "west-X" }},
        {"image changed", "CLONE_SOURCE_CHANGED", func(x *cloneFixture, _ *cloud.Plan, _ *cloud.Offer) { x.image = "different-image" }},
        {"source started", "CLONE_SOURCE_NOT_STOPPED", func(x *cloneFixture, _ *cloud.Plan, _ *cloud.Offer) { x.sourceStatus = "running" }},
        {"already cloning", "CLONE_SOURCE_NOT_STOPPED", func(x *cloneFixture, _ *cloud.Plan, _ *cloud.Offer) { x.sourceStatus = "shutdown_cloning" }},
        {"price increased", "OFFER_CHANGED", func(x *cloneFixture, _ *cloud.Plan, _ *cloud.Offer) { x.price = "3100" }},
        {"smaller disk", "BAD_REQUEST", func(_ *cloneFixture, p *cloud.Plan, _ *cloud.Offer) { p.AutoDL.ExpandDataDiskGB = 149 }},
    } {
        t.Run(tc.name, func(t *testing.T) {
            x := newCloneFixture(t, false)
            plan := x.plan(t)
            o := cloneOffer()
            tc.change(x, &plan, o)
            _, err := x.client.Clone(context.Background(), plan, o)
            var api *cloud.APIError
            if !errors.As(err, &api) || api.Code != tc.code || api.Unconfirmed || x.clones != 0 || x.creates != 0 {
                t.Fatal(err, x.clones)
            }
        })
    }
}

func TestCloneAmbiguousMutationDoesNotReplay(t *testing.T) {
    for _, failure := range []string{"drop", "same"} {
        x := newCloneFixture(t, false)
        p := x.plan(t)
        x.failure = failure
        _, err := x.client.Clone(context.Background(), p, cloneOffer())
        var api *cloud.APIError
        if !errors.As(err, &api) || !api.Unconfirmed || x.clones != 1 || x.creates != 0 {
            t.Fatal(err, x.clones)
        }
    }
}

func TestCloneFreezesPrivateImageIdentity(t *testing.T) {
    x := newCloneFixture(t, false)
    x.imageType = "private"
    x.private = "private-original"
    x.image = ""
    plan := x.plan(t)
    if plan.Clone.Image != "private-original" {
        t.Fatal(plan.Clone)
    }
    x.private = "private-replaced"
    _, err := x.client.Clone(context.Background(), plan, cloneOffer())
    var api *cloud.APIError
    if !errors.As(err, &api) || api.Code != "CLONE_SOURCE_CHANGED" || x.clones != 0 {
        t.Fatal(err, x.clones)
    }
}

func TestCloneCopyStatusesAndCUDACompatibility(t *testing.T) {
    for raw, want := range map[string]string{"Succeed": "succeeded", "Running": "copying", "Queued": "copying", "Failed": "failed", "future": "unknown", "": ""} {
        data, _ := json.Marshal(map[string]any{"copy_data_disk_after_clone_status": raw})
        if got := cloneDataStatus(object{"additional_info": data}); got != want {
            t.Fatal(raw, got)
        }
    }
    for _, tc := range []struct {
        host, required string
        want           bool
    }{{"13.2", "12.8", true}, {"12.10", "12.8", true}, {"12.8", "12.8", true}, {"12.7", "12.8", false}, {"", "12.8", false}, {"future", "12.8", false}, {"", "", true}} {
        if got := cudaCompatible(tc.host, tc.required); got != tc.want {
            t.Fatal(tc, got)
        }
    }
}
