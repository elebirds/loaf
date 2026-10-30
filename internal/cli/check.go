package cli

import (
    "fmt"
    "io"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
)

func printCheckBatch(out io.Writer, r checkBatch, l *i18n.Localizer) {
    uxBPrintCheckBatch(out, r, l)
}

func printResourceObservation(out io.Writer, r *ssh.ResourceObservation, l *i18n.Localizer) {
    if !r.ObservedAt.IsZero() {
        fmt.Fprintln(out, l.Text("probe_observed", map[string]any{"Value": r.ObservedAt.Format(time.RFC3339)}))
    }
    fmt.Fprintln(out, l.Text("probe_cpu", map[string]any{"Count": probeValue(r.CPUCount, l)}))
    fmt.Fprintln(out, l.Text("probe_memory", map[string]any{"Total": probeGiB(r.MemoryTotalBytes, l), "Bytes": probeGiB(r.MemoryAvailableBytes, l)}))
    for _, gpu := range r.GPUs {
        utilization := l.Text("probe_unknown")
        if gpu.UtilizationPercent != nil {
            utilization = fmt.Sprintf("%.1f", *gpu.UtilizationPercent)
        }
        fmt.Fprintln(out, l.Text("probe_gpu_stats", map[string]any{"Count": gpu.Index, "Value": gpu.Model, "Bytes": probeGiB(gpu.MemoryUsedBytes, l), "Total": probeGiB(gpu.MemoryTotalBytes, l), "Matched": utilization}))
    }
    if len(r.Unavailable) > 0 {
        labels := make([]string, 0, len(r.Unavailable))
        for _, component := range r.Unavailable {
            labels = append(labels, l.Text("probe_component_"+component))
        }
        fmt.Fprintln(out, l.Text("probe_unavailable", map[string]any{"Value": strings.Join(labels, ", ")}))
    }
}

func probeValue[T any](value *T, l *i18n.Localizer) string {
    if value == nil {
        return l.Text("probe_unknown")
    }
    return fmt.Sprint(*value)
}

func probeGiB(value *int64, l *i18n.Localizer) string {
    if value == nil {
        return l.Text("probe_unknown")
    }
    return fmt.Sprintf("%.2f", float64(*value)/(1<<30))
}
