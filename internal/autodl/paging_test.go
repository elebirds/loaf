package autodl

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
)

func TestOrdinarySearchWindowsKeepBudgetAndNonalignedOffset(t *testing.T) {
    for _, tc := range []struct{ offset, limit, total int }{{0, 150, 301}, {150, 150, 301}, {300, 150, 301}, {4, 1, 6}} {
        t.Run(fmt.Sprint(tc), func(t *testing.T) {
            calls := 0
            f := factory(t, func(r *http.Request) *http.Response {
                calls++
                if !strings.HasSuffix(r.URL.Path, "/machine/list") {
                    t.Fatal(r.URL)
                }
                var body object
                _ = json.NewDecoder(r.Body).Decode(&body)
                page, size := *integer(body, "page_index"), *integer(body, "page_size")
                rows := []string{}
                for i := (page - 1) * size; i < min(page*size, tc.total); i++ {
                    rows = append(rows, fmt.Sprintf(`{"machine_id":"m-%d","gpu_idle_num":0}`, i))
                }
                return response(200, fmt.Sprintf(`{"code":"Success","data":{"list":[%s],"result_total":%d}}`, strings.Join(rows, ","), tc.total))
            })
            p, err := f.Open(context.Background(), "demo", account("standard"), "standard")
            if err != nil {
                t.Fatal(err)
            }
            request := plan()
            request.Requirements.GPUCount = 1
            page, err := p.(cloud.PagedInventory).SearchWindow(context.Background(), request, tc.limit, tc.offset)
            want := min(tc.limit, tc.total-tc.offset)
            if err != nil || page.Examined != want || len(page.Offers) != want || page.Offers[0].ID != fmt.Sprintf("m-%d", tc.offset) || page.Complete && tc.offset > 0 || calls > 3 {
                t.Fatal(page, err, calls)
            }
            more := tc.offset+want < tc.total
            if page.HasMore != more || more && page.NextOffset != tc.offset+want {
                t.Fatal(page)
            }
        })
    }
}
