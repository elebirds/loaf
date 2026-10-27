package client

import (
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path"
    "slices"
    "sync"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

// CheckInput selects bounded independent target observations.
type CheckInput struct {
    Target  *string
    Targets []string
    Timeout time.Duration
    Runner  bool
    Report  *string
    Policy  CallPolicy
}

// CheckResult carries a native error for presentation and a safe classified failure.
type CheckResult struct {
    Target, ProjectID string
    Report            ssh.Report
    Runner            *execution.RunnerInfo
    Steps             map[string]string
    ElapsedMillis     int64
    Error             error
    FailureKind       string
}

// CheckBatch preserves completed, failed and unexamined rows.
type CheckBatch struct {
    Checks                                  []CheckResult
    Selected, Completed, Failed, Unexamined int
}

// CheckOutcome preserves single and multi-target result shapes.
type CheckOutcome struct {
    Single *CheckResult
    Batch  *CheckBatch
}

// Check validates every selected alias before starting bounded observations.
func (c *Machines) Check(ctx context.Context, in CheckInput) (CheckOutcome, error) {
    multiple := in.Targets != nil
    if in.Timeout <= 0 || (multiple && (len(in.Targets) == 0 || len(in.Targets) > 32 || in.Target != nil)) || (in.Report != nil && *in.Report == "") {
        return CheckOutcome{}, fault.New("BAD_CONFIG", "check_batch_flags", nil)
    }
    if in.Report != nil {
        if err := validateDiagnosticPath(*in.Report); err != nil {
            return CheckOutcome{}, err
        }
        c.Trace.Enable()
    }
    root, file, err := config.Discover(ctx, c.CWD)
    if err != nil {
        return CheckOutcome{}, err
    }
    project, err := c.State.Existing(root)
    if err != nil {
        return CheckOutcome{}, err
    }
    names := in.Targets
    if !multiple {
        name, _, err := config.Resolve(value(in.Target), project, file, c.Settings)
        if err != nil {
            return CheckOutcome{}, err
        }
        names = []string{name}
    }
    for i, name := range names {
        if !config.ValidName(name) || slices.Contains(names[:i], name) {
            return CheckOutcome{}, fault.New("BAD_CONFIG", "check_batch_flags", nil)
        }
        if _, _, err := config.Resolve(name, project, file, c.Settings); err != nil {
            return CheckOutcome{}, err
        }
    }
    if c.Checker == nil {
        return CheckOutcome{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, err = c.State.Select(ctx, root, "")
    if err != nil {
        return CheckOutcome{}, err
    }
    checks := CheckBatch{Checks: make([]CheckResult, len(names)), Selected: len(names)}
    examined := make([]bool, len(names))
    jobs := make(chan int)
    var workers sync.WaitGroup
    count := 1
    if in.Policy.Batch {
        count = 4
    }
    for range min(count, len(names)) {
        workers.Go(func() {
            for i := range jobs {
                name := names[i]
                row := CheckResult{Target: name, ProjectID: project.ID, Steps: map[string]string{"connect": "unconfirmed", "authentication": "unconfirmed", "platform": "not_checked", "runner": "not_checked"}}
                checks.Checks[i] = row
                if ctx.Err() != nil {
                    continue
                }
                examined[i] = true
                target := c.Settings.Targets[name]
                dir := file.RemoteDir
                if dir == "" {
                    dir = path.Join(target.RemoteBase, project.ID)
                }
                request, cancel := context.WithTimeout(ctx, in.Timeout)
                started := time.Now()
                row.Report, row.Error = c.Checker.Check(request, target, dir, in.Policy.Batch)
                if row.Report.OS != "" {
                    row.Steps["connect"], row.Steps["authentication"], row.Steps["platform"] = "verified", "verified", "verified"
                    if row.Report.OS != "Linux" || (row.Report.Arch != "x86_64" && row.Report.Arch != "aarch64") {
                        row.Steps["platform"] = "unsupported"
                    }
                }
                if row.Error == nil && in.Runner {
                    if c.Runner == nil {
                        row.Error = fault.New("BUILD_REQUIRED", "runner_missing", nil)
                    } else {
                        info, err := c.Runner.Verify(request, target, row.Report.Arch, in.Policy.Batch)
                        row.Error = err
                        if err == nil {
                            row.Runner = &info
                            row.Steps["runner"] = "verified"
                        }
                    }
                    if row.Error != nil {
                        row.Steps["runner"] = "failed"
                    }
                }
                row.ElapsedMillis = time.Since(started).Milliseconds()
                if row.Error != nil {
                    row.FailureKind = ssh.FailureKind(row.Error)
                    if request.Err() != nil {
                        row.FailureKind = "observation_timeout"
                        if ctx.Err() != nil {
                            row.FailureKind = "canceled"
                        }
                    }
                    if row.FailureKind == "authentication" {
                        row.Steps["authentication"] = "failed"
                    }
                }
                cancel()
                checks.Checks[i] = row
            }
        })
    }
    for i := range names {
        jobs <- i
    }
    close(jobs)
    workers.Wait()
    for i, row := range checks.Checks {
        if !examined[i] {
            checks.Unexamined++
        } else if row.Error != nil {
            checks.Failed++
        } else {
            checks.Completed++
        }
    }
    result := CheckOutcome{Batch: &checks}
    if !multiple {
        result = CheckOutcome{Single: &checks.Checks[0]}
    }
    if in.Report != nil {
        encoded, err := json.MarshalIndent(safeDiagnostic(checks, c.Trace.Snapshot()), "", "    ")
        if err != nil {
            return result, err
        }
        if err := atomicfile.Write(*in.Report, append(encoded, '\n')); err != nil {
            return result, fault.Wrap("REPORT_FAILED", "diagnostic_report_failed", err)
        }
    }
    if !multiple {
        if checks.Unexamined > 0 {
            return result, fault.Wrap("OBSERVATION_CANCELED", "observation_canceled", ctx.Err())
        }
        return result, checks.Checks[0].Error
    }
    if checks.Failed > 0 || checks.Unexamined > 0 {
        return result, fault.New("CHECK_PARTIAL", "check_batch_partial", nil)
    }
    return result, nil
}

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

func safeDiagnostic(batch CheckBatch, trace *ssh.TraceReport) diagnosticReport {
    result := diagnosticReport{1, "loaf_diagnostics", wire.Version, time.Now().UTC(), []diagnosticCheck{}, trace}
    for i, row := range batch.Checks {
        outcome := "ok"
        if row.Error != nil {
            outcome = "error"
        }
        if row.Report.OS == "" && row.Error == nil {
            outcome = "unexamined"
        }
        item := diagnosticCheck{TargetIndex: i + 1, Outcome: outcome, Steps: row.Steps, ElapsedMillis: row.ElapsedMillis}
        if row.Report.OS == "Linux" {
            item.OS = "Linux"
        }
        if row.Report.Arch == "x86_64" || row.Report.Arch == "aarch64" {
            item.Arch = row.Report.Arch
        }
        if row.Error != nil {
            item.Code = "QUERY_FAILED"
            var f *fault.Error
            if errors.As(row.Error, &f) {
                item.Code = f.Code
            }
            item.Stage = fault.Stage(row.Error)
            item.FailureKind = row.FailureKind
        }
        result.Checks = append(result.Checks, item)
    }
    return result
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
    decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&old) != nil || decoder.Decode(new(any)) != io.EOF || old.FormatVersion != 1 || old.Kind != "loaf_diagnostics" {
        return failure()
    }
    return nil
}
