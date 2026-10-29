package cloud

import (
    "github.com/elebirds/loaf/internal/fault"
    "strings"
)

// ValidateAcquisition checks locally decidable creation conditions before consent.
// Live stock, price and clone source checks remain with their owning adapters.
func ValidateAcquisition(p Plan, mode string) error {
    if mode != "session" && mode != "developer" {
        return invalid()
    }
    if mode == "developer" && p.Product == "standard" {
        return Failure(&APIError{Code: "UNSUPPORTED_AUTH_PRODUCT"})
    }
    if err := ValidateAcquisitionWindow(p); err != nil {
        return err
    }
    if p.Clone != nil {
        return validateClone(p, p.Clone.Instance.Account)
    }
    if p.AutoDL.Image == "" && p.AutoDL.PrivateImageUUID == "" {
        return fault.Invalid("--image", "missing_argument", "cloud_image_required")
    }
    if p.AutoDL.Image != "" && p.AutoDL.PrivateImageUUID != "" {
        return fault.Invalid("--image", "conflicting_arguments", "cloud_image_conflict")
    }
    if p.Product == "pro" && p.AutoDL.CUDAFrom <= 0 {
        return fault.Invalid("--cuda-from", "missing_argument", "cloud_cuda_required")
    }
    if p.Product == "pro" && mode == "developer" && p.AutoDL.GPUSpecUUID == "" {
        return fault.Invalid("--gpu-spec", "missing_argument", "cloud_spec_required")
    }
    return validateCreate(p, mode)
}

// ValidateAcquisitionWindow checks selection and budget conditions even when a
// clone's live source metadata has not been obtained yet.
func ValidateAcquisitionWindow(p Plan) error {
    if !ValidProduct(p.Product) || len(p.OfferID) > 512 || strings.ContainsAny(p.OfferID, "\x00\r\n\t") {
        return invalid()
    }
    if p.OfferID != "" && (p.Continuous || p.WaitTimeout != "0s") {
        return fault.Invalid("--offer", "conflicting_arguments", "cloud_offer_wait")
    }
    if (p.Continuous || p.OfferID != "") && p.Requirements.MaxComputeHourlyPrice == nil {
        return fault.Invalid("--max-compute-hourly-price", "missing_argument", "cloud_budget_required")
    }
    if (p.Continuous || p.OfferID != "") && (p.AutoDL.ExpandDataDiskGB > 0 || p.AutoDL.ExpandSystemDiskGB > 0) && p.Requirements.MaxStorageDailyPrice == nil {
        return fault.Invalid("--max-storage-daily-price", "missing_argument", "cloud_storage_budget_required")
    }
    return nil
}
