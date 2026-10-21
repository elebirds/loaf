package cloud

import (
    "encoding/json"
)

// Compare semantic inventory values, not timestamps. JSON here is a transient
// comparison key, not an on-disk manifest or a hash/integrity mechanism.
func observationKey(r SearchResult) string {
    // Build only fields which determine the observation's meaning.
    values := make([]any, 0, len(r.Matches))
    for _, o := range r.Matches {
        values = append(values, []any{o.ID, o.Vendor, o.Model, o.GPUCount, o.GPUMemoryGiB, o.CPUs, o.MemoryGiB, o.DiskGiB, o.Region, o.Available, o.ComputeHourlyPrice})
    }
    data, _ := json.Marshal([]any{values, r.Rejected, r.Examined, r.Complete})
    return string(data)
}
