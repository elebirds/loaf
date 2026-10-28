package cli

import (
    "fmt"
    "io"
    "strings"

    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
)

func printResult(out io.Writer, data any, l *i18n.Localizer, ok bool) error {
    if handled, err := printAsync(out, data, l); handled {
        return err
    }
    if printCloud(out, data, l) {
        return nil
    }
    switch r := data.(type) {
    case versionResult:
        _, err := fmt.Fprintf(out, "loaf %s (%s/%s)\n", r.Version, r.OS, r.Arch)
        return err
    case logAcknowledgement:
        if !r.Acknowledged {
            _, err := fmt.Fprintf(out, "%s  %s\n", r.RunID, l.Text("run_state_save_failed", map[string]any{"Code": r.StateError}))
            return err
        }
        _, err := fmt.Fprintln(out, l.Text("log_acknowledged", map[string]any{"ID": r.RunID, "Offset": r.Offset}))
        return err
    case addResult:
        fmt.Fprintln(out, l.Text("added", map[string]any{"Target": r.Target, "Destination": r.Destination}))
    case targetsResult:
        printTargets(out, r, l, false)
    case machineDetails:
        printTargets(out, r.targetsResult, l, true)
    case removeResult:
        fmt.Fprintln(out, l.Text("remove_done", map[string]any{"Target": r.Target}))
        if r.Selected {
            fmt.Fprintln(out, l.Text("remove_reselect"))
        }
    case useResult:
        fmt.Fprintln(out, l.Text("selected", map[string]any{"Root": r.Root, "Target": r.Target}))
    case syncResult:
        printSync(out, r, l)
    case pullBatch:
        for _, row := range r.Runs {
            printPull(out, row.Result, l, row.Error == nil)
            if row.Error != nil {
                fmt.Fprintln(out, row.Error.Code+": "+row.Error.Message)
            }
        }
        fmt.Fprintln(out, l.Text("pull_batch_summary", map[string]any{"Count": r.Completed, "Total": r.Selected, "Value": r.Failed, "Matched": r.Unexamined}))
    case execution.RunPreview:
        printPreview(out, r, l)
    case execution.OutputListing:
        printOutputs(out, r, l)
    case sources.PullResult:
        printPull(out, r, l, ok)
    case cleanBatch:
        printClean(out, r, l)
    case execution.LocalDetails:
        printLocalDetails(out, r, l)
    case execution.View:
        printRun(out, r, l)
    case execution.WaitResult:
        for _, v := range r.Runs {
            printRun(out, v, l)
        }
        if r.TimedOut {
            fmt.Fprintln(out, l.Text("wait_timed_out"))
        }
        fmt.Fprintln(out, l.Text("batch_state_errors", map[string]any{"Count": r.StateErrors, "Total": r.Unexamined}))
        fmt.Fprintln(out, l.Text("wait_summary", map[string]any{"Count": r.Completed, "Total": r.Selected, "Value": r.QueryErrors, "Matched": r.Examined, "Bytes": r.Interrupted}))
    case runList:
        if r.Source == "local" {
            fmt.Fprintln(out, l.Text("inspect_local"))
        }
        for _, run := range r.Runs {
            printRunRow(out, run, l)
        }
        if len(r.Runs) == 0 {
            if len(r.Filters.Statuses) > 0 || r.Filters.Target != "" || r.Filters.Name != "" || len(r.Filters.Tags) > 0 || r.Filters.Since != nil || r.Filters.Until != nil {
                fmt.Fprintln(out, l.Text("ps_no_matches"))
            } else {
                fmt.Fprintln(out, l.Text("runs_empty"))
            }
        }
        if len(r.Filters.Statuses) > 0 || r.Filters.Target != "" || r.Filters.Name != "" || len(r.Filters.Tags) > 0 || r.Filters.Since != nil || r.Filters.Until != nil || r.Truncated {
            fmt.Fprintln(out, l.Text("ps_scan_summary", map[string]any{"Count": r.Examined, "Total": r.Eligible, "Matched": r.Matched}))
        }
        if r.Unresolved > 0 {
            fmt.Fprintln(out, l.Text("ps_unresolved"))
        }
        if r.Truncated {
            fmt.Fprintln(out, l.Text("runs_truncated"))
        }
        fmt.Fprintln(out, l.Text("batch_state_errors", map[string]any{"Count": r.StateErrors, "Total": max(0, r.Eligible-r.Examined)}))
    case checkBatch:
        printCheckBatch(out, r, l)
    case checkResult:
        fmt.Fprintln(out, l.Text("check_heading", map[string]any{"Target": r.Target}))
        if r.Report.OS == "" {
            return nil
        }
        fmt.Fprintf(out, "%s: %s / %s\n%s: %s\n", l.Text("platform"), r.Report.OS, r.Report.Arch, l.Text("remote_directory"), r.Report.RemoteDir)
        w := "no"
        if r.Report.Writable {
            w = "yes"
        }
        fmt.Fprintf(out, "%s: %s\n%s: %.2f GiB\n", l.Text("writable"), l.Text(w), l.Text("disk"), float64(r.Report.DiskAvailableBytes)/(1<<30))
        for _, group := range []struct {
            id    string
            tools map[string]ssh.Tool
        }{{"local_tools", r.Report.Local}, {"remote_tools", r.Report.Remote}} {
            fmt.Fprintln(out, l.Text(group.id)+":")
            for _, name := range []string{"ssh", "rsync", "python", "uv", "nvidia-smi", "nvcc"} {
                tool, exists := group.tools[name]
                if !exists {
                    continue
                }
                value := tool.Path
                if value == "" {
                    value = l.Text("missing")
                }
                fmt.Fprintf(out, "  %s: %s\n", name, value)
                if tool.Version != "" {
                    fmt.Fprintf(out, "    %s\n", strings.ReplaceAll(tool.Version, "\n", "\n    "))
                }
            }
        }
        if r.Report.Resources != nil {
            printResourceObservation(out, r.Report.Resources, l)
        }
        if r.Report.GPU != "" && (r.Report.Resources == nil || len(r.Report.Resources.GPUs) == 0) {
            fmt.Fprintf(out, "%s: %s\n", l.Text("gpu"), r.Report.GPU)
        }
        if r.Runner != nil {
            fmt.Fprintln(out, l.Text("runner_verified", map[string]any{"Version": r.Runner.Version, "Path": r.Runner.Path}))
        }
        for _, issue := range r.Report.Issues {
            fmt.Fprintln(out, "- "+l.Text(issue))
        }
        if ok {
            fmt.Fprintln(out, l.Text("check_ok"))
        }
    default:
        return fmt.Errorf("unsupported command result %T", data)
    }
    return nil
}
