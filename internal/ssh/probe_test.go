package ssh

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
)

func TestResourceProbePreservesUnitsAndUnknowns(t *testing.T) {
    r := parseResourceObservation([]string{"16", "33554432", "16777216", "0, NVIDIA A100, 81920, 1024, 0\n1, NVIDIA A100, 81920, [N/A], [N/A]"})
    if r.CPUCount == nil || *r.CPUCount != 16 || *r.MemoryTotalBytes != 32<<30 || *r.MemoryAvailableBytes != 16<<30 || len(r.GPUs) != 2 {
        t.Fatalf("%+v", r)
    }
    if r.GPUs[0].UtilizationPercent == nil || *r.GPUs[0].UtilizationPercent != 0 || *r.GPUs[0].MemoryUsedBytes != 1<<30 {
        t.Fatalf("%+v", r.GPUs[0])
    }
    if r.GPUs[1].MemoryUsedBytes != nil || r.GPUs[1].UtilizationPercent != nil || r.ObservedAt.IsZero() {
        t.Fatalf("%+v", r)
    }
    b, err := json.Marshal(r)
    if err != nil || !strings.Contains(string(b), `"memory_used_bytes":null`) || !strings.Contains(string(b), `"utilization_percent":0`) {
        t.Fatal(string(b), err)
    }
}

func TestProbeScriptProducesStructuredObservationAndMinimalSkipsGPU(t *testing.T) {
    bin := t.TempDir()
    write := func(name, body string) string {
        path := filepath.Join(bin, name)
        if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
            t.Fatal(err)
        }
        return path
    }
    ssh := write("ssh", `if [ "$1" = -V ]; then echo fixture-ssh >&2; exit 0; fi
for arg do command=$arg; done
exec /bin/sh -c "$command"
`)
    write("uname", `case "$1" in -s) echo Linux ;; -m) echo aarch64 ;; esac
`)
    write("rsync", "echo 'rsync fixture'\n")
    write("getconf", "echo 16\n")
    calls := filepath.Join(t.TempDir(), "gpu-calls")
    t.Setenv("LOAF_TEST_GPU_CALLS", calls)
    write("nvidia-smi", `echo call >> "$LOAF_TEST_GPU_CALLS"
case "$1" in
--version) echo driver-fixture ;;
--query-gpu=name,driver_version,memory.total) echo 'Fixture GPU, fixture-driver, 8192' ;;
*) echo '0, Fixture GPU, 8192, 1024, 20' ;;
esac
`)
    t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
    c := &Client{Binary: ssh}
    target := config.Target{Destination: "fixture"}
    root := t.TempDir()
    report, err := c.Check(context.Background(), target, root, true)
    if err != nil || report.Resources == nil || report.Resources.CPUCount == nil || *report.Resources.CPUCount != 16 || len(report.Resources.GPUs) != 1 {
        t.Fatalf("%+v %v", report, err)
    }
    if *report.Resources.GPUs[0].MemoryTotalBytes != 8<<30 {
        t.Fatal(report.Resources.GPUs[0])
    }
    if err := os.Remove(calls); err != nil {
        t.Fatal(err)
    }
    report, err = c.Preflight(context.Background(), target, root, true)
    if err != nil || report.Resources != nil {
        t.Fatalf("%+v %v", report, err)
    }
    if _, err := os.Stat(calls); !os.IsNotExist(err) {
        t.Fatal("minimal preflight invoked GPU probe", err)
    }
}

func TestResourceProbePartialFailureDoesNotInventZero(t *testing.T) {
    for _, fields := range [][]string{{"", "", "", ""}, {"-1", "invalid", "0", "private driver error"}, {"0", "9223372036854775807", "-1", "0, Model, [N/A], [N/A], NaN"}} {
        r := parseResourceObservation(fields)
        if r.CPUCount != nil || r.MemoryTotalBytes != nil || len(r.Unavailable) == 0 {
            t.Fatalf("%+v", r)
        }
        if len(r.GPUs) > 0 && r.GPUs[0].UtilizationPercent != nil {
            t.Fatal("invented utilization")
        }
    }
}
