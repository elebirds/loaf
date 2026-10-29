package autodl

import (
    "context"
    "encoding/json"
    "strings"
    "time"
    "unicode"

    "github.com/elebirds/loaf/internal/cloud"
)

// Catalog follows the public console directories (2026-10-29):
// service-code.30826265.js, system-image...d3e67626.js, myImage.682e0f65.js.
// These requests are read-only, including the POST image filters.
func (s *Standard) Catalog(ctx context.Context, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    return s.api.catalog(ctx, in)
}

func (p *WebPro) Catalog(ctx context.Context, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    if in.Category == "region" {
        out := cloud.CatalogPage{Category: "region", Items: []cloud.CatalogItem{}, Complete: false, ObservedAt: time.Now().UTC()}
        out.Source = "autodl:pro:web:console-regions"
        if in.Limit < 1 || in.Limit > 1000 {
            return out, &cloud.APIError{Code: "BAD_REQUEST"}
        }
        for _, id := range []string{"westDC3", "beijingDC2"} {
            out.Examined++
            if len(out.Items) < in.Limit && strings.Contains(strings.ToLower(id), strings.ToLower(in.Query)) {
                out.Items = append(out.Items, cloud.CatalogItem{ID: id, Name: id})
            }
        }
        return out, nil
    }
    if in.Category == "gpu" {
        out := cloud.CatalogPage{Category: "gpu", Items: []cloud.CatalogItem{}, Complete: true, ObservedAt: time.Now().UTC()}
        out.Source = "autodl:pro:web:gpu_type"
        if in.Limit < 1 || in.Limit > 1000 {
            return out, &cloud.APIError{Code: "BAD_REQUEST"}
        }
        raw, err := p.request(ctx, "POST", "/api/v1/machine_pro/gpu_type", map[string]any{"data_center_list": []string{"westDC3", "beijingDC2"}}, false)
        if err != nil {
            return out, err
        }
        var rows []object
        if json.Unmarshal(raw, &rows) != nil || rows == nil {
            return out, &cloud.APIError{Code: "INVALID_RESPONSE"}
        }
        for _, row := range rows {
            id := text(row, "gpu_type")
            if id == "" || strings.IndexFunc(id, unicode.IsControl) >= 0 {
                return out, &cloud.APIError{Code: "INVALID_RESPONSE"}
            }
            out.Examined++
            if !strings.Contains(strings.ToLower(id), strings.ToLower(in.Query)) {
                continue
            }
            if len(out.Items) >= in.Limit {
                out.Complete = false
                continue
            }
            out.Items = append(out.Items, cloud.CatalogItem{ID: id, Name: id})
        }
        return out, nil
    }
    return p.api.catalog(ctx, in)
}

func (a *api) catalog(ctx context.Context, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    out := cloud.CatalogPage{Category: in.Category, Items: []cloud.CatalogItem{}, Complete: true, ObservedAt: time.Now().UTC()}
    if in.Limit < 1 || in.Limit > 1000 {
        return out, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    add := func(item cloud.CatalogItem) {
        out.Examined++
        item.Name = strings.Map(func(r rune) rune {
            if unicode.IsControl(r) {
                return -1
            }
            return r
        }, item.Name)
        if in.Query != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.ID), strings.ToLower(in.Query)) {
            return
        }
        if len(out.Items) >= in.Limit {
            out.Complete = false
            return
        }
        out.Items = append(out.Items, item)
    }
    var path, method string
    var body any = map[string]string{}
    switch in.Category {
    case "gpu":
        path, method = "/api/v1/machine/gpu_type", "GET"
    case "region":
        path, method = "/api/v1/common", "GET"
    case "image":
        if in.Kind == "community" {
            return out, &cloud.APIError{Code: "UNSUPPORTED_CAPABILITY"}
        }
        path, method = "/api/v1/image/all", "POST"
        if in.Kind == "private" {
            path = "/api/v1/image/private/get"
        } else if in.Kind != "" && in.Kind != "base" {
            return out, &cloud.APIError{Code: "BAD_REQUEST"}
        }
        vendors, arches := []string{}, []string{}
        if in.Vendor != "" {
            vendors = append(vendors, in.Vendor)
        }
        if in.Architecture != "" {
            arches = append(arches, in.Architecture)
        }
        body = map[string]any{"cpu_arch": arches, "chip_corp": vendors}
    default:
        return out, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    raw, err := a.request(ctx, method, path, body, false)
    out.Source = "autodl:" + a.product + ":web:" + path
    if err != nil {
        return out, err
    }
    invalid := func() (cloud.CatalogPage, error) { return out, &cloud.APIError{Code: "INVALID_RESPONSE"} }
    switch in.Category {
    case "gpu":
        var rows []object
        if json.Unmarshal(raw, &rows) != nil || rows == nil {
            return invalid()
        }
        for _, row := range rows {
            n := text(row, "gpu_name")
            if n == "" || strings.IndexFunc(n, unicode.IsControl) >= 0 {
                return invalid()
            }
            add(cloud.CatalogItem{ID: n, Name: n, Vendor: text(row, "chip_corp")})
        }
    case "region":
        var data struct {
            Regions []object `json:"region_list"`
        }
        if json.Unmarshal(raw, &data) != nil || data.Regions == nil {
            return invalid()
        }
        for _, row := range data.Regions {
            id, n := text(row, "region_sign"), text(row, "region_name")
            dc := text(row, "data_center")
            if !validID.MatchString(id) || n == "" || dc != "" && !validID.MatchString(dc) {
                return invalid()
            }
            item := cloud.CatalogItem{ID: id, Name: n, DataCenter: dc}
            visibility, usage := text(row, "visible"), text(row, "used_for")
            if visibility != "" && usage != "" {
                public := visibility == "all" && usage == "all"
                item.Public = &public
            }
            add(item)
        }
    case "image":
        if in.Kind == "private" {
            var rows []object
            if json.Unmarshal(raw, &rows) != nil || rows == nil {
                return invalid()
            }
            for _, row := range rows {
                id, n := text(row, "image_uuid"), text(row, "name")
                if !validID.MatchString(id) || n == "" {
                    return invalid()
                }
                add(cloud.CatalogItem{ID: id, Name: n, Kind: "private", CUDA: catalogVersion(row, "cuda_v"), Vendor: text(row, "chip_corp"), Architecture: text(row, "cpu_arch")})
            }
        } else {
            var nodes []imageNode
            if json.Unmarshal(raw, &nodes) != nil || nodes == nil {
                return invalid()
            }
            if err := walkImages(nodes, nil, add); err != nil {
                return out, err
            }
        }
    }
    return out, nil
}

type imageNode struct {
    Label    json.RawMessage `json:"label_name"`
    Children []imageNode     `json:"children"`
}

func walkImages(nodes []imageNode, labels []string, add func(cloud.CatalogItem)) error {
    if len(labels) > 3 {
        return &cloud.APIError{Code: "INVALID_RESPONSE"}
    }
    for _, n := range nodes {
        if n.Children != nil {
            var label string
            if json.Unmarshal(n.Label, &label) != nil {
                return &cloud.APIError{Code: "INVALID_RESPONSE"}
            }
            next := append(append([]string{}, labels...), label)
            if err := walkImages(n.Children, next, add); err != nil {
                return err
            }
        } else {
            var leaf object
            if json.Unmarshal(n.Label, &leaf) != nil || len(labels) != 3 || !validID.MatchString(text(leaf, "uuid")) {
                return &cloud.APIError{Code: "INVALID_RESPONSE"}
            }
            cuda := catalogVersion(leaf, "v")
            add(cloud.CatalogItem{ID: text(leaf, "uuid"), Name: strings.Join(append(append([]string{}, labels...), "CUDA "+cuda), " / "), Kind: "base", Framework: labels[0], FrameworkVersion: labels[1], Python: labels[2], CUDA: cuda, Vendor: text(leaf, "chip_corp"), Architecture: text(leaf, "cpu_arch")})
        }
    }
    return nil
}

func catalogVersion(row object, key string) string {
    if value := text(row, key); value != "" {
        return value
    }
    var value json.Number
    if json.Unmarshal(row[key], &value) == nil {
        return value.String()
    }
    return ""
}
