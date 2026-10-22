package cloud

import (
    "strings"

    "github.com/elebirds/loaf/internal/fault"
)

// PrepareClone confines an allocation to its source region and inherited image.
// Callers supply the source disk size as the lowest-priority profile default;
// an explicit/configured smaller disk is rejected rather than silently raised.
func PrepareClone(plan Plan, source CloneSource) (Plan, error) {
    if err := source.validate(); err != nil {
        return plan, err
    }
    r := plan.Requirements
    if plan.Product != source.Instance.Product || len(r.Regions) > 0 && (len(r.Regions) != 1 || r.Regions[0] != source.Region) {
        return plan, fault.New("BAD_CONFIG", "cloud_clone_region", nil)
    }
    if plan.AutoDL.ExpandDataDiskGB < source.ExpandDataDiskGB {
        return plan, fault.New("BAD_CONFIG", "cloud_clone_disk", nil)
    }
    if r.GPUVendor != "" && !strings.EqualFold(r.GPUVendor, source.Vendor) {
        return plan, fault.New("BAD_CONFIG", "cloud_clone_options", nil)
    }
    plan.Requirements.Regions = []string{source.Region}
    plan.Requirements.GPUVendor = source.Vendor
    plan.AutoDL = ImageOptions{ExpandDataDiskGB: plan.AutoDL.ExpandDataDiskGB}
    plan.Clone = &source
    return plan, nil
}

func (s CloneSource) validate() error {
    if s.Instance.Provider != "autodl" || s.Instance.Product != "standard" ||
        !localName.MatchString(s.Instance.Account) || !platformID.MatchString(s.Instance.ID) ||
        s.Region == "" || s.Image == "" || s.Vendor == "" || s.Architecture == "" ||
        s.ExpandDataDiskGB < 0 || strings.ContainsAny(s.Region+s.Image+s.Vendor+s.Architecture+s.CUDA, "\x00\r\n") {
        return invalid()
    }
    return nil
}

func validateClone(plan Plan, account string) error {
    source := plan.Clone
    if source == nil || source.validate() != nil || source.Instance.Account != account ||
        plan.Product != source.Instance.Product || len(plan.Requirements.Regions) != 1 || plan.Requirements.Regions[0] != source.Region ||
        !strings.EqualFold(plan.Requirements.GPUVendor, source.Vendor) || plan.AutoDL.ExpandDataDiskGB < source.ExpandDataDiskGB ||
        plan.AutoDL != (ImageOptions{ExpandDataDiskGB: plan.AutoDL.ExpandDataDiskGB}) {
        return invalid()
    }
    return nil
}
