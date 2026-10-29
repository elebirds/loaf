package cloud

func profilePointer[T any](value T) *T { return &value }

// ProfileFromPlan keeps effective resource preferences, never a resource,
// credential, reservation, payment approval, or clone source identity.
func ProfileFromPlan(p Plan) Profile {
    r := p.Requirements
    out := Profile{GPUVendor: profilePointer(r.GPUVendor), GPUModels: append([]string{}, r.GPUModels...), GPUCount: profilePointer(r.GPUCount), MinGPUMemoryGiB: profilePointer(r.MinGPUMemoryGiB), MinCPUs: profilePointer(r.MinCPUs), MinMemoryGiB: profilePointer(r.MinMemoryGiB), MinDiskGiB: profilePointer(r.MinDiskGiB), Regions: append([]string{}, r.Regions...), Currency: profilePointer("CNY"), WaitTimeout: profilePointer(p.WaitTimeout), PollInterval: profilePointer(p.PollInterval), Continuous: profilePointer(p.Continuous)}
    if p.Product != "" {
        out.Product = profilePointer(p.Product)
    }
    out.AutoDL = AutoDLProfile{Image: profilePointer(p.AutoDL.Image), PrivateImageUUID: profilePointer(p.AutoDL.PrivateImageUUID), GPUSpecID: profilePointer(p.AutoDL.GPUSpecID), GPUSpecUUID: profilePointer(p.AutoDL.GPUSpecUUID), CUDAFrom: profilePointer(p.AutoDL.CUDAFrom), ExpandSystemDiskGB: profilePointer(p.AutoDL.ExpandSystemDiskGB), ExpandDataDiskGB: profilePointer(p.AutoDL.ExpandDataDiskGB)}
    out.MaxComputeHourlyPrice, out.MaxStorageDailyPrice = profilePointer(""), profilePointer("")
    if r.MaxComputeHourlyPrice != nil {
        out.MaxComputeHourlyPrice = profilePointer(r.MaxComputeHourlyPrice.Amount)
    }
    if r.MaxStorageDailyPrice != nil {
        out.MaxStorageDailyPrice = profilePointer(r.MaxStorageDailyPrice.Amount)
    }
    return out
}
