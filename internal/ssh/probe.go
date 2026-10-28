package ssh

import (
    "encoding/csv"
    "math"
    "slices"
    "strconv"
    "strings"
    "time"
)

// ResourceObservation contains optional point-in-time hardware observations.
// Missing or unparsable values remain nil rather than being reported as zero.
type ResourceObservation struct {
    ObservedAt           time.Time        `json:"observed_at"`
    CPUCount             *int             `json:"cpu_count"`
    MemoryTotalBytes     *int64           `json:"memory_total_bytes"`
    MemoryAvailableBytes *int64           `json:"memory_available_bytes"`
    GPUs                 []GPUObservation `json:"gpus"`
    Unavailable          []string         `json:"unavailable"`
}

// GPUObservation is a bounded read of one card, not an allocation guarantee.
type GPUObservation struct {
    Index              int      `json:"index"`
    Model              string   `json:"model"`
    MemoryTotalBytes   *int64   `json:"memory_total_bytes"`
    MemoryUsedBytes    *int64   `json:"memory_used_bytes"`
    UtilizationPercent *float64 `json:"utilization_percent"`
}

func parseResourceObservation(fields []string) *ResourceObservation {
    r := &ResourceObservation{ObservedAt: time.Now().UTC(), GPUs: []GPUObservation{}, Unavailable: []string{}}
    if len(fields) != 4 {
        r.Unavailable = []string{"cpu", "memory", "gpu"}
        return r
    }
    if count, err := strconv.Atoi(strings.TrimSpace(fields[0])); err == nil && count > 0 {
        r.CPUCount = &count
    } else {
        r.Unavailable = append(r.Unavailable, "cpu")
    }
    r.MemoryTotalBytes = probeBytes(fields[1], 1024)
    r.MemoryAvailableBytes = probeBytes(fields[2], 1024)
    if r.MemoryTotalBytes == nil || *r.MemoryTotalBytes == 0 {
        r.MemoryTotalBytes = nil
        r.Unavailable = append(r.Unavailable, "memory_total")
    }
    if r.MemoryAvailableBytes == nil {
        r.Unavailable = append(r.Unavailable, "memory_available")
    }
    records, err := csv.NewReader(strings.NewReader(fields[3])).ReadAll()
    if err != nil || len(records) == 0 || len(records) > 64 {
        r.Unavailable = append(r.Unavailable, "gpu")
        return r
    }
    for _, record := range records {
        if len(record) != 5 {
            r.Unavailable = appendMissing(r.Unavailable, "gpu")
            continue
        }
        index, err := strconv.Atoi(strings.TrimSpace(record[0]))
        if err != nil || index < 0 {
            r.Unavailable = appendMissing(r.Unavailable, "gpu")
            continue
        }
        gpu := GPUObservation{Index: index, Model: strings.TrimSpace(record[1]), MemoryTotalBytes: probeBytes(record[2], 1<<20), MemoryUsedBytes: probeBytes(record[3], 1<<20)}
        if gpu.MemoryTotalBytes == nil {
            r.Unavailable = appendMissing(r.Unavailable, "gpu_memory")
        }
        if gpu.MemoryUsedBytes == nil {
            r.Unavailable = appendMissing(r.Unavailable, "gpu_memory")
        }
        if utilization, err := strconv.ParseFloat(strings.TrimSpace(record[4]), 64); err == nil && !math.IsNaN(utilization) && utilization >= 0 && utilization <= 100 {
            gpu.UtilizationPercent = &utilization
        } else {
            r.Unavailable = appendMissing(r.Unavailable, "gpu_utilization")
        }
        r.GPUs = append(r.GPUs, gpu)
    }
    return r
}

func appendMissing(values []string, value string) []string {
    if !slices.Contains(values, value) {
        return append(values, value)
    }
    return values
}

func probeBytes(text string, unit int64) *int64 {
    value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
    if err != nil || value < 0 || value > math.MaxInt64/unit {
        return nil
    }
    value *= unit
    return &value
}
