package cli

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "path"
    "slices"
    "sync"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
    "github.com/spf13/cobra"
)

type checkBatch struct {
    Checks     []checkResult `json:"checks"`
    Selected   int           `json:"selected"`
    Completed  int           `json:"completed"`
    Failed     int           `json:"failed"`
    Unexamined int           `json:"unexamined"`
}

func newCheckCommand(o Options, cfg config.Global, target *string, l *i18n.Localizer, result func(any)) *cobra.Command {
    var timeout time.Duration
    var verifyRunner bool
    var names []string
    var reportPath string
    cmd := &cobra.Command{Use: "check", Short: l.Text("check_short"), Args: arguments(0, 0)}
    cmd.Flags().BoolVar(&verifyRunner, "runner", false, l.Text("flag_runner"))
    cmd.Flags().DurationVar(&timeout, "timeout", 20*time.Second, l.Text("flag_timeout"))
    cmd.Flags().StringSliceVar(&names, "targets", nil, l.Text("flag_check_targets"))
    cmd.Flags().StringVar(&reportPath, "report", "", l.Text("flag_check_report"))
    cmd.RunE = func(cmd *cobra.Command, _ []string) error {
        multiple := cmd.Flags().Changed("targets")
        if timeout <= 0 || (multiple && (len(names) == 0 || len(names) > 32 || cmd.Flags().Changed("target"))) || (cmd.Flags().Changed("report") && reportPath == "") {
            return fault.New("BAD_CONFIG", "check_batch_flags", nil)
        }
        if reportPath != "" {
            if err := validateDiagnosticPath(reportPath); err != nil {
                return err
            }
            o.Trace.Enable()
        }
        root, file, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return err
        }
        p, err := o.Store.Existing(root)
        if err != nil {
            return err
        }
        if !multiple {
            name, _, err := config.Resolve(*target, p, file, cfg)
            if err != nil {
                return err
            }
            names = []string{name}
        }
        for i, name := range names {
            if !config.ValidName(name) || slices.Contains(names[:i], name) {
                return fault.New("BAD_CONFIG", "check_batch_flags", nil)
            }
            if _, _, err := config.Resolve(name, p, file, cfg); err != nil {
                return err
            }
        }
        p, err = o.Store.Select(cmd.Context(), root, "")
        if err != nil {
            return err
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        batch := jsonMode || !o.Interactive
        checks := checkBatch{Checks: make([]checkResult, len(names)), Selected: len(names)}
        errs := make([]error, len(names))
        examined := make([]bool, len(names))
        jobs := make(chan int)
        var workers sync.WaitGroup
        count := 1
        if batch {
            count = 4
        }
        for range min(count, len(names)) {
            workers.Add(1)
            go func() {
                defer workers.Done()
                for i := range jobs {
                    name := names[i]
                    checked := checkResult{Target: name, ProjectID: p.ID, Steps: map[string]string{"connect": "unconfirmed", "authentication": "unconfirmed", "platform": "not_checked", "runner": "not_checked"}}
                    checks.Checks[i] = checked
                    if cmd.Context().Err() != nil {
                        continue
                    }
                    examined[i] = true
                    t := cfg.Targets[name]
                    dir := file.RemoteDir
                    if dir == "" {
                        dir = path.Join(t.RemoteBase, p.ID)
                    }
                    request, cancel := context.WithTimeout(cmd.Context(), timeout)
                    started := time.Now()
                    observed, err := o.Checker.Check(request, t, dir, batch)
                    checked.Report = observed
                    if checked.Report.OS != "" {
                        checked.Steps["connect"], checked.Steps["authentication"] = "verified", "verified"
                        checked.Steps["platform"] = "verified"
                        if checked.Report.OS != "Linux" || (checked.Report.Arch != "x86_64" && checked.Report.Arch != "aarch64") {
                            checked.Steps["platform"] = "unsupported"
                        }
                    }
                    if err == nil && verifyRunner {
                        if o.Runner == nil {
                            err = fault.New("BUILD_REQUIRED", "runner_missing", nil)
                        } else {
                            var info execution.RunnerInfo
                            info, err = o.Runner.Verify(request, t, checked.Report.Arch, batch)
                            if err == nil {
                                checked.Runner = &info
                                checked.Steps["runner"] = "verified"
                            }
                        }
                        if err != nil {
                            checked.Steps["runner"] = "failed"
                        }
                    }
                    checked.ElapsedMillis = time.Since(started).Milliseconds()
                    if err != nil {
                        kind := ssh.FailureKind(err)
                        if request.Err() != nil {
                            kind = "observation_timeout"
                            if cmd.Context().Err() != nil {
                                kind = "canceled"
                            }
                        }
                        checked.Error = batchError(err, l)
                        checked.Error.FailureKind = kind
                        if kind == "authentication" {
                            checked.Steps["authentication"] = "failed"
                        }
                    }
                    cancel()
                    checks.Checks[i] = checked
                    errs[i] = err
                }
            }()
        }
        for i := range names {
            jobs <- i
        }
        close(jobs)
        workers.Wait()
        for i, checked := range checks.Checks {
            if !examined[i] {
                checks.Unexamined++
            } else if checked.Error != nil {
                checks.Failed++
            } else {
                checks.Completed++
            }
        }
        if multiple {
            result(checks)
        } else {
            result(checks.Checks[0])
        }
        if reportPath != "" {
            report := safeDiagnostic(checks, o.Trace.Snapshot())
            data, err := json.MarshalIndent(report, "", "    ")
            if err != nil {
                return err
            }
            if err := atomicfile.Write(reportPath, append(data, '\n')); err != nil {
                return fault.Wrap("REPORT_FAILED", "diagnostic_report_failed", err)
            }
        }
        if !multiple {
            if checks.Unexamined > 0 {
                return fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", cmd.Context().Err())
            }
            return errs[0]
        }
        if checks.Failed > 0 || checks.Unexamined > 0 {
            return fault.New("CHECK_PARTIAL", "check_batch_partial", nil)
        }
        return nil
    }
    return cmd
}

// Only this allowlisted projection is exported, never check reports/diagnostics.
type diagnosticCheck struct {
    TargetIndex   int               `json:"target_index"`
    Outcome       string            `json:"outcome"`
    Steps         map[string]string `json:"steps"`
    Code          string            `json:"code,omitempty"`
    Stage         string            `json:"stage,omitempty"`
    FailureKind   string            `json:"failure_kind,omitempty"`
    OS            string            `json:"os,omitempty"`
    Arch          string            `json:"arch,omitempty"`
    ElapsedMillis int64             `json:"elapsed_millis"`
}
type diagnosticReport struct {
    FormatVersion int               `json:"format_version"`
    Kind          string            `json:"kind"`
    Version       string            `json:"version"`
    CreatedAt     time.Time         `json:"created_at"`
    Checks        []diagnosticCheck `json:"checks"`
    Trace         *ssh.TraceReport  `json:"trace,omitempty"`
}

func safeDiagnostic(r checkBatch, trace *ssh.TraceReport) diagnosticReport {
    report := diagnosticReport{1, "loaf_diagnostics", wire.Version, time.Now().UTC(), []diagnosticCheck{}, trace}
    for i, row := range r.Checks {
        outcome := "ok"
        if row.Error != nil {
            outcome = "error"
        }
        if row.Report.OS == "" && row.Error == nil {
            outcome = "unexamined"
        }
        d := diagnosticCheck{TargetIndex: i + 1, Outcome: outcome, Steps: row.Steps, ElapsedMillis: row.ElapsedMillis}
        // Platform labels are constrained to known values; do not echo arbitrary
        // server output into a diagnostic intended for sharing.
        if row.Report.OS == "Linux" {
            d.OS = "Linux"
        }
        if row.Report.Arch == "x86_64" || row.Report.Arch == "aarch64" {
            d.Arch = row.Report.Arch
        }
        if row.Error != nil {
            d.Code, d.Stage, d.FailureKind = row.Error.Code, row.Error.Stage, row.Error.FailureKind
        }
        report.Checks = append(report.Checks, d)
    }
    return report
}
func validateDiagnosticPath(name string) error {
    info, err := os.Lstat(name)
    if errors.Is(err, os.ErrNotExist) {
        return nil
    }
    failure := func() error { return fault.New("BAD_CONFIG", "diagnostic_report_invalid", nil) }
    if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
        return failure()
    }
    f, err := os.Open(name)
    if err != nil {
        return failure()
    }
    defer f.Close()
    var old diagnosticReport
    d := json.NewDecoder(io.LimitReader(f, 1<<20))
    d.DisallowUnknownFields()
    if d.Decode(&old) != nil || d.Decode(new(any)) != io.EOF || old.FormatVersion != 1 || old.Kind != "loaf_diagnostics" {
        return failure()
    }
    return nil
}
func printCheckBatch(out io.Writer, r checkBatch, l *i18n.Localizer) {
    for _, row := range r.Checks {
        fmt.Fprintln(out, l.Text("check_heading", map[string]any{"Target": row.Target}))
        for _, step := range []string{"connect", "authentication", "platform", "runner"} {
            fmt.Fprintf(out, "  %s: %s\n", l.Text("check_step_"+step), l.Text("check_step_state_"+row.Steps[step]))
        }
        if row.Error != nil {
            fmt.Fprintln(out, row.Error.Code+"  "+l.Text("trace_reason_"+row.Error.FailureKind))
        }
    }
    fmt.Fprintln(out, l.Text("check_batch_summary", map[string]any{"Count": r.Completed, "Total": r.Selected, "Value": r.Failed, "Matched": r.Unexamined}))
}
