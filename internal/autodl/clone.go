package autodl

import (
    "context"
    "encoding/json"
    "strconv"
    "strings"

    "github.com/elebirds/loaf/internal/cloud"
)

func (s *Standard) cloneSource(ctx context.Context, ref cloud.Ref) (cloud.CloneSource, object, object, error) {
    var source cloud.CloneSource
    row, err := s.listedInstance(ctx, ref)
    if err != nil {
        return source, nil, nil, err
    }
    if text(row, "status") != "shutdown" && text(row, "status") != "stopped" {
        return source, nil, nil, &cloud.APIError{Code: "CLONE_SOURCE_NOT_STOPPED"}
    }
    raw, err := s.request(ctx, "GET", "/api/v1/instance/snapshot", map[string]string{"instance_uuid": ref.ID}, false)
    if err != nil {
        return source, nil, nil, err
    }
    var snapshot, image object
    if json.Unmarshal(raw, &snapshot) != nil || json.Unmarshal(snapshot["image"], &image) != nil {
        return source, nil, nil, &cloud.APIError{Code: "INVALID_RESPONSE"}
    }
    expand := integer(row, "expand_data_disk_size")
    imageID := text(image, "image_name")
    if text(image, "image_type") != "public" {
        if private := text(row, "private_image_uuid"); private != "" {
            imageID = private
        } else if reproduction := text(row, "reproduction_uuid"); reproduction != "" {
            imageID = reproduction
        }
    }
    if expand == nil || int64(*expand)%(1<<30) != 0 || text(row, "region_sign") == "" || !validID.MatchString(imageID) || text(image, "chip_corp") == "" || text(image, "cpu_arch") == "" {
        return source, nil, nil, &cloud.APIError{Code: "CLONE_SOURCE_INVALID"}
    }
    source = cloud.CloneSource{Instance: ref, Region: text(row, "region_sign"), Image: imageID,
        Vendor: strings.ToLower(text(image, "chip_corp")), Architecture: text(image, "cpu_arch"), CUDA: text(image, "cuda_version"), ExpandDataDiskGB: *expand >> 30}
    return source, row, snapshot, nil
}

// CloneSource reads the stopped source without changing its power state.
func (s *Standard) CloneSource(ctx context.Context, ref cloud.Ref) (cloud.CloneSource, error) {
    source, _, _, err := s.cloneSource(ctx, ref)
    return source, err
}

// Clone revalidates the source and price, then submits exactly one system- and
// data-disk clone. A response lost after this request is never replayed here.
func (s *Standard) Clone(ctx context.Context, plan cloud.Plan, offer *cloud.Offer) (cloud.Ref, error) {
    source := plan.Clone
    if source == nil || offer == nil || !validID.MatchString(offer.ID) || offer.Region != source.Region ||
        source.Instance != s.ref(source.Instance.ID) || plan.Product != "standard" ||
        len(plan.Requirements.Regions) != 1 || plan.Requirements.Regions[0] != source.Region ||
        plan.AutoDL != (cloud.ImageOptions{ExpandDataDiskGB: plan.AutoDL.ExpandDataDiskGB}) ||
        plan.AutoDL.ExpandDataDiskGB < source.ExpandDataDiskGB || len(cloud.Match(*offer, plan.Requirements)) != 0 {
        return cloud.Ref{}, &cloud.APIError{Code: "BAD_REQUEST"}
    }
    fresh, row, snapshot, err := s.cloneSource(ctx, source.Instance)
    if err != nil {
        return cloud.Ref{}, creationReadFailure(err)
    }
    if fresh != *source {
        return cloud.Ref{}, &cloud.APIError{Code: "CLONE_SOURCE_CHANGED"}
    }
    var image object
    _ = json.Unmarshal(snapshot["image"], &image)
    base := ""
    if text(image, "image_type") == "public" {
        base = source.Image
    }
    private := text(row, "private_image_uuid")
    reproduction := text(row, "reproduction_uuid")
    if base == "" && private == "" && reproduction == "" || base != "" && !validID.MatchString(base) || private != "" && !validID.MatchString(private) || reproduction != "" && !validID.MatchString(reproduction) {
        return cloud.Ref{}, &cloud.APIError{Code: "CLONE_SOURCE_INVALID"}
    }
    compute, storage, err := s.quote(ctx, plan, offer.ID)
    if err != nil {
        return cloud.Ref{}, creationReadFailure(err)
    }
    if err := checkPrice(compute, plan.Requirements); err != nil {
        return cloud.Ref{}, err
    }
    if err := checkStorage(storage, plan.Requirements); err != nil {
        return cloud.Ref{}, err
    }
    info := map[string]any{
        "machine_id": offer.ID, "charge_type": "payg", "req_gpu_amount": plan.Requirements.GPUCount,
        "base_image_uuid": base, "private_image_uuid": private, "reproduction_uuid": reproduction,
        "instance_name": "", "cg_application_uuid": text(snapshot, "cg_application_uuid"),
        "cg_application_info": map[string]any{}, "reproduction_id": 0, "ft_sparse": false,
        "expand_data_disk":           int64(plan.AutoDL.ExpandDataDiskGB) << 30,
        "copy_data_disk_after_clone": true, "keep_src_user_service_address_after_clone": false,
    }
    if v, ok := snapshot["cg_application_info"]; ok && string(v) != "null" {
        info["cg_application_info"] = v
    }
    if v, ok := row["reproduction_id"]; ok && string(v) != "null" {
        info["reproduction_id"] = v
    }
    body := map[string]any{"instance_uuid": source.Instance.ID, "instance_info": info,
        "price_info": map[string]any{"coupon_id_list": []string{}, "machine_id": offer.ID, "charge_type": "payg", "duration": 1, "num": plan.Requirements.GPUCount, "expand_data_disk": int64(plan.AutoDL.ExpandDataDiskGB) << 30}}
    raw, err := s.request(ctx, "POST", "/api/v1/order/instance/clone/payg", body, true)
    ref, err := created(s.api, raw, err)
    if err == nil && ref == source.Instance {
        return cloud.Ref{}, &cloud.APIError{Code: "INVALID_RESPONSE", Unconfirmed: true}
    }
    return ref, err
}

// cudaCompatible compares catalog major/minor versions, not decimal numbers.
func cudaCompatible(host, required string) bool {
    if required == "" {
        return true
    }
    parse := func(s string) (int, int, bool) {
        parts := strings.Split(s, ".")
        if len(parts) != 2 {
            return 0, 0, false
        }
        major, e1 := strconv.Atoi(parts[0])
        minor, e2 := strconv.Atoi(parts[1])
        return major, minor, e1 == nil && e2 == nil && major > 0 && minor >= 0
    }
    hm, hn, hOK := parse(host)
    rm, rn, rOK := parse(required)
    return hOK && rOK && (hm > rm || hm == rm && hn >= rn)
}

func cloneDataStatus(o object) string {
    var extra object
    if json.Unmarshal(o["additional_info"], &extra) != nil {
        return ""
    }
    status := text(extra, "copy_data_disk_after_clone_status")
    switch status {
    case "Succeed":
        return "succeeded"
    case "Running", "Queued":
        return "copying"
    case "Failed":
        return "failed"
    case "":
        return ""
    default:
        return "unknown"
    }
}

var _ cloud.Cloner = (*Standard)(nil)
